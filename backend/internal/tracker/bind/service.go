// Package bind is the tracker BIND service: it links one Series to one
// tracker's reading-progress entry (a TrackBinding row), keeps that row's
// snapshot fresh on demand (FetchTrack), and undoes the link (Unbind,
// optionally deleting the remote entry too). It is the per-series half of
// the tracker subsystem — internal/tracker/connect is the per-ACCOUNT half
// (login/token storage); see spec/trackers-oauth-phase3 §4.
//
// This package (unlike internal/tracker itself) DOES use ent — it is the
// "subpkg that CAN use ent" internal/tracker's own doc comment calls out,
// mirroring internal/tracker/connect and internal/metadatasvc: an
// ent-touching orchestration layer sits ABOVE the ent-free port package,
// never the reverse.
//
// DURABILITY: every mutation here writes the DB row FIRST, then mirrors the
// series' current full binding SET into its tsundoku.json sidecar (see
// disk.WriteTrackBindings) — best-effort, mirroring
// internal/metadatasvc.persist's own sidecar-write discipline (a series
// with no folder on disk yet simply has no sidecar to write; the DB row is
// enough until the first chapter lands). disk.Reconcile's
// restoreTrackBindings reads that block back after a total DB loss.
// 🔴 Tokens are NEVER sidecar'd — see disk.TrackBindingSidecar's own doc
// comment; only WHICH tracker entry a series is bound to survives a wipe,
// never the account credential.
package bind

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// Sentinel errors. The HTTP handler layer (slice 3c) maps these to their
// documented status codes.
var (
	// ErrTrackerNotFound is returned when trackerID does not match any
	// tracker in the Service's Registry.
	ErrTrackerNotFound = errors.New("bind: unknown tracker id")
	// ErrTrackerNotConnected is returned when trackerID has no
	// TrackerConnection row (the owner has never logged in, or logged
	// out) — every method here that talks to a tracker needs an account
	// token first.
	ErrTrackerNotConnected = errors.New("bind: tracker is not connected")
	// ErrSeriesNotFound is returned by Bind when seriesID matches no
	// Series row.
	ErrSeriesNotFound = errors.New("bind: series not found")
	// ErrBindingNotFound is returned by Unbind/FetchTrack when recordID
	// matches no TrackBinding row.
	ErrBindingNotFound = errors.New("bind: binding not found")
)

// Service is the tracker bind service.
type Service struct {
	client   *ent.Client
	registry *tracker.Registry
	storage  string
}

// NewService builds a Service. storage is the library storage root (for the
// sidecar durability writes — see the package doc comment).
func NewService(client *ent.Client, registry *tracker.Registry, storage string) *Service {
	return &Service{client: client, registry: registry, storage: storage}
}

// Bind links seriesID to trackerID's remoteID entry: it loads the
// connected account's token, resolves the remote entry via GetEntry —
// registering a FRESH one via SaveEntry when the manga is not yet on the
// account's list at all (so the account has something to sync against
// later, mirroring how binding on Suwayomi/Komikku both create-if-absent) —
// and upserts a TrackBinding row (UNIQUE series_id+tracker_id: binding the
// same series to the same tracker twice re-binds it to the new remoteID
// rather than erroring) populated from the resolved entry's
// status/last_chapter_read/total_chapters/score/library_id/dates. The
// current full binding set is then mirrored into the series' sidecar
// (best-effort; see the package doc comment).
//
// Returns ErrTrackerNotFound / ErrTrackerNotConnected / ErrSeriesNotFound
// for their respective conditions; any other error is a genuine
// upstream/persistence failure — a GetEntry/SaveEntry failure is NOT
// best-effort here, since the owner explicitly asked to bind.
func (s *Service) Bind(ctx context.Context, seriesID uuid.UUID, trackerID int, remoteID string) (*ent.TrackBinding, error) {
	t, ok := s.registry.ByID(trackerID)
	if !ok {
		return nil, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, trackerID)
	if err != nil {
		return nil, err
	}

	exists, err := s.client.Series.Query().Where(entseries.IDEQ(seriesID)).Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("bind: check series %s: %w", seriesID, err)
	}
	if !exists {
		return nil, ErrSeriesNotFound
	}

	entry, err := t.GetEntry(ctx, token, remoteID)
	if err != nil {
		return nil, fmt.Errorf("bind: fetch remote entry from %s: %w", t.Key(), err)
	}
	if entry == nil {
		created, saveErr := t.SaveEntry(ctx, token, tracker.TrackEntry{RemoteID: remoteID})
		if saveErr != nil {
			return nil, fmt.Errorf("bind: create remote entry on %s: %w", t.Key(), saveErr)
		}
		entry = &created
	}

	binding, err := s.upsertBinding(ctx, seriesID, trackerID, remoteID, *entry)
	if err != nil {
		return nil, err
	}

	s.syncSidecarBestEffort(ctx, seriesID)
	return binding, nil
}

// Unbind removes recordID's TrackBinding row. When deleteRemote is true, it
// FIRST calls DeleteEntry against the tracker's own account (a genuine
// remote deletion — fails the whole call, and leaves the local row intact,
// on any error) so a partial unbind can never silently leave the remote
// list entry orphaned while the owner believes it is gone; deleteRemote
// false only ever touches the local row. The series' sidecar is
// re-synchronized (the remaining binding set, best-effort) after a
// successful delete.
//
// Returns ErrBindingNotFound for an unknown recordID, and (when
// deleteRemote) ErrTrackerNotFound / ErrTrackerNotConnected for a binding
// whose tracker has since been unregistered or disconnected.
func (s *Service) Unbind(ctx context.Context, recordID uuid.UUID, deleteRemote bool) error {
	binding, err := s.client.TrackBinding.Query().Where(enttrackbinding.IDEQ(recordID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrBindingNotFound
		}
		return fmt.Errorf("bind: load binding %s: %w", recordID, err)
	}

	if deleteRemote {
		if err := s.deleteRemoteEntry(ctx, binding); err != nil {
			return err
		}
	}

	if err := s.client.TrackBinding.DeleteOne(binding).Exec(ctx); err != nil {
		return fmt.Errorf("bind: delete binding %s: %w", recordID, err)
	}

	s.syncSidecarBestEffort(ctx, binding.SeriesID)
	return nil
}

// deleteRemoteEntry is Unbind's deleteRemote=true path, extracted so Unbind
// itself stays under the fleet's per-function complexity budget.
func (s *Service) deleteRemoteEntry(ctx context.Context, binding *ent.TrackBinding) error {
	t, ok := s.registry.ByID(binding.TrackerID)
	if !ok {
		return ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, binding.TrackerID)
	if err != nil {
		return err
	}
	entry := tracker.TrackEntry{RemoteID: binding.RemoteID, LibraryID: binding.LibraryID}
	if err := t.DeleteEntry(ctx, token, entry); err != nil {
		return fmt.Errorf("bind: delete remote entry on %s: %w", t.Key(), err)
	}
	return nil
}

// FetchTrack re-pulls recordID's binding from its tracker's own account
// (GetEntry) and writes the fresh status/last_chapter_read/total_chapters/
// score/library_id/dates onto the TrackBinding row, then re-syncs the
// series' sidecar (best-effort). When the remote entry has since vanished
// from the account's list (GetEntry returns nil, nil — a valid state, not
// an error), the existing row is returned UNCHANGED rather than silently
// zeroed: the owner can explicitly Unbind if the drift is intentional.
//
// Returns ErrBindingNotFound / ErrTrackerNotFound / ErrTrackerNotConnected
// for their respective conditions.
func (s *Service) FetchTrack(ctx context.Context, recordID uuid.UUID) (*ent.TrackBinding, error) {
	binding, err := s.client.TrackBinding.Query().Where(enttrackbinding.IDEQ(recordID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrBindingNotFound
		}
		return nil, fmt.Errorf("bind: load binding %s: %w", recordID, err)
	}

	t, ok := s.registry.ByID(binding.TrackerID)
	if !ok {
		return nil, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, binding.TrackerID)
	if err != nil {
		return nil, err
	}

	entry, err := t.GetEntry(ctx, token, binding.RemoteID)
	if err != nil {
		return nil, fmt.Errorf("bind: fetch remote entry from %s: %w", t.Key(), err)
	}
	if entry == nil {
		return binding, nil
	}

	updated, err := s.upsertBinding(ctx, binding.SeriesID, binding.TrackerID, binding.RemoteID, *entry)
	if err != nil {
		return nil, err
	}

	s.syncSidecarBestEffort(ctx, binding.SeriesID)
	return updated, nil
}

// SearchTracker runs an AUTHED search against trackerID's connected
// account (spec §4's "authed search via the account token" — every tracker
// here accepts a token even where its own search endpoint would work
// anonymously, so the API surface behaves consistently regardless of which
// tracker is asked).
//
// Returns ErrTrackerNotFound / ErrTrackerNotConnected for their respective
// conditions.
func (s *Service) SearchTracker(ctx context.Context, trackerID int, query string) ([]tracker.TrackSearchResult, error) {
	t, ok := s.registry.ByID(trackerID)
	if !ok {
		return nil, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, trackerID)
	if err != nil {
		return nil, err
	}
	results, err := t.Search(ctx, token, query)
	if err != nil {
		return nil, fmt.Errorf("bind: search %s: %w", t.Key(), err)
	}
	return results, nil
}

// accountToken loads trackerID's connected account's access token.
// ErrTrackerNotConnected when the owner has never logged in (or logged
// out) — every authenticated operation in this service needs one.
func (s *Service) accountToken(ctx context.Context, trackerID int) (string, error) {
	conn, err := s.client.TrackerConnection.Query().
		Where(enttrackerconnection.TrackerID(trackerID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrTrackerNotConnected
		}
		return "", fmt.Errorf("bind: query tracker connection: %w", err)
	}
	return conn.AccessToken, nil
}

// upsertBinding finds or creates the TrackBinding row for
// (seriesID, trackerID) and writes entry's fields onto it wholesale — the
// query-then-create/update pattern this codebase's other find-or-create
// call sites use (mirrors category.FindOrCreate,
// disk.findOrCreateSeriesProvider). remote_url is deterministically
// (re)computed from trackerID+remoteID via remoteURLFor on every call —
// see that helper's own doc comment for why it does not come from
// tracker.TrackEntry.
func (s *Service) upsertBinding(ctx context.Context, seriesID uuid.UUID, trackerID int, remoteID string, entry tracker.TrackEntry) (*ent.TrackBinding, error) {
	remoteURL := remoteURLFor(trackerID, remoteID)

	existing, err := s.client.TrackBinding.Query().
		Where(enttrackbinding.SeriesID(seriesID), enttrackbinding.TrackerID(trackerID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("bind: query track binding (series=%s tracker=%d): %w", seriesID, trackerID, err)
	}

	if existing != nil {
		updated, uerr := existing.Update().
			SetRemoteID(remoteID).
			SetRemoteURL(remoteURL).
			SetLibraryID(entry.LibraryID).
			SetStatus(entry.Status).
			SetLastChapterRead(entry.Progress).
			SetTotalChapters(entry.TotalChapters).
			SetScore(entry.Score).
			SetNillableStartDate(entry.StartDate).
			SetNillableFinishDate(entry.FinishDate).
			SetPrivate(entry.Private).
			Save(ctx)
		if uerr != nil {
			return nil, fmt.Errorf("bind: update track binding (series=%s tracker=%d): %w", seriesID, trackerID, uerr)
		}
		return updated, nil
	}

	created, err := s.client.TrackBinding.Create().
		SetSeriesID(seriesID).
		SetTrackerID(trackerID).
		SetRemoteID(remoteID).
		SetRemoteURL(remoteURL).
		SetLibraryID(entry.LibraryID).
		SetStatus(entry.Status).
		SetLastChapterRead(entry.Progress).
		SetTotalChapters(entry.TotalChapters).
		SetScore(entry.Score).
		SetNillableStartDate(entry.StartDate).
		SetNillableFinishDate(entry.FinishDate).
		SetPrivate(entry.Private).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("bind: create track binding (series=%s tracker=%d): %w", seriesID, trackerID, err)
	}
	return created, nil
}

// remoteURLFor best-effort constructs the canonical link to a bound
// remote entry from well-known, deterministic per-tracker URL patterns —
// tracker.TrackEntry carries no URL field of its own (unlike
// tracker.TrackSearchResult, whose caller already had one from the pick
// step), so this is derived here rather than plumbed through the whole
// GetEntry/SaveEntry round-trip. AniList/MAL ids resolve directly to a
// stable page URL; Kitsu's real manga URL needs a SLUG this port never
// sees from a bare remote id, and MangaUpdates' own list-series responses
// don't carry per-item state RemoteID for a template — both degrade to ""
// (the owner can still reach the entry by searching the tracker directly;
// this is display provenance, never correctness-critical).
func remoteURLFor(trackerID int, remoteID string) string {
	switch trackerID {
	case tracker.IDAniList:
		return "https://anilist.co/manga/" + remoteID
	case tracker.IDMAL:
		return "https://myanimelist.net/manga/" + remoteID
	default:
		return ""
	}
}

// syncSidecarBestEffort mirrors seriesID's CURRENT full set of TrackBinding
// rows into its tsundoku.json sidecar (disk.WriteTrackBindings) — always a
// full re-read-then-write of every binding, never a single-row patch, so an
// Unbind's removed entry is correctly dropped from disk too. A series with
// no folder yet (disk.ErrNoSeriesDir) is the expected common case, not a
// fault — logged at Debug; any other disk error is logged at Warn, never
// fatal to the caller (mirrors metadatasvc.writeSidecarBestEffort's same
// non-fatal discipline for the SAME sidecar file).
func (s *Service) syncSidecarBestEffort(ctx context.Context, seriesID uuid.UUID) {
	row, err := s.client.Series.Query().Where(entseries.IDEQ(seriesID)).WithCategory().Only(ctx)
	if err != nil {
		slog.WarnContext(ctx, "bind: sidecar sync: load series failed", "series_id", seriesID, "err", err)
		return
	}
	bindings, err := s.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		slog.WarnContext(ctx, "bind: sidecar sync: load bindings failed", "series_id", seriesID, "err", err)
		return
	}

	blocks := make([]disk.TrackBindingSidecar, 0, len(bindings))
	for _, b := range bindings {
		blocks = append(blocks, disk.TrackBindingSidecar{
			TrackerID:       b.TrackerID,
			RemoteID:        b.RemoteID,
			RemoteURL:       b.RemoteURL,
			Status:          b.Status,
			LastChapterRead: b.LastChapterRead,
			Score:           b.Score,
		})
	}

	seriesDir := disk.SeriesDir(s.storage, category.NameOf(row), row.Title)
	if err := disk.WriteTrackBindings(seriesDir, blocks); err != nil {
		if errors.Is(err, disk.ErrNoSeriesDir) {
			slog.DebugContext(ctx, "bind: sidecar not written: series has no folder on disk",
				"series_id", seriesID, "title", row.Title)
			return
		}
		slog.WarnContext(ctx, "bind: sidecar write failed", "series_id", seriesID, "err", err)
	}
}
