package syncsvc

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// SyncNow pulls EVERY one of seriesID's TrackBinding entries from its
// tracker's own account (GetEntry) and converges local↔remote per the
// umbrella spec §6 "conflict = MAX wins BOTH directions" rule
// (sync.Converge), extended to a THREE-WAY convergence that also folds in
// the local LIBRARY's own read-count (seriesLocalFurthest) — not just the
// binding's stored value — so a series read far ahead locally (e.g. right
// after adopting a backlog, or right after a fresh Bind) converges to that
// real progress immediately, the same "converge on add" behavior the
// reference apps (Suwayomi/Komikku) have. Whichever of the three
// (local-library read-count / binding's stored value / remote's reported
// progress) is furthest ahead wins; see syncOneBinding for the exact
// never-regress chain. When local was strictly ahead, the converged value
// is ALSO pushed back to the remote (reusing sync.NextPush's own decision)
// so both sides genuinely end up in agreement, not just the local row.
//
// One binding's failure (GetEntry/UpdateEntry error, unregistered/
// disconnected tracker) is logged and that binding's PRE-SYNC row is kept
// in the result unchanged — it never aborts syncing the series' other
// bindings. A hard error is returned only if the initial binding-set query
// itself fails.
func (s *Service) SyncNow(ctx context.Context, seriesID uuid.UUID) ([]*ent.TrackBinding, error) {
	bindings, err := s.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("syncsvc: SyncNow: load bindings for series %s: %w", seriesID, err)
	}

	out := make([]*ent.TrackBinding, 0, len(bindings))
	for _, b := range bindings {
		updated, syncErr := s.syncOneBinding(ctx, b)
		if syncErr != nil {
			slog.WarnContext(ctx, "syncsvc: SyncNow: binding sync failed",
				"track_binding_id", b.ID, "series_id", seriesID, "err", syncErr)
			out = append(out, b)
			continue
		}
		out = append(out, updated)
	}
	return out, nil
}

// syncOneBinding pulls b's remote entry and converges it with the local row.
// A nil GetEntry result (the manga has since vanished from the account's
// list — a valid empty state, not an error, mirroring bind.Service.
// FetchTrack) leaves b unchanged: the owner can explicitly Unbind if the
// drift is intentional.
func (s *Service) syncOneBinding(ctx context.Context, b *ent.TrackBinding) (*ent.TrackBinding, error) {
	t, ok := s.registry.ByID(b.TrackerID)
	if !ok {
		return nil, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, b.TrackerID)
	if err != nil {
		return nil, err
	}

	remote, err := t.GetEntry(ctx, token, b.RemoteID)
	if err != nil {
		s.markExpiredOnTokenFailure(ctx, b.TrackerID, err)
		return nil, fmt.Errorf("syncsvc: fetch remote entry from %s for binding %s: %w", t.Key(), b.ID, err)
	}
	if remote == nil {
		return b, nil
	}

	localFurthest, err := s.seriesLocalFurthest(ctx, b.SeriesID)
	if err != nil {
		return nil, err
	}

	// Three-way max-wins convergence (umbrella spec §6 "MAX wins both
	// directions", extended to fold in the LOCAL LIBRARY's own read-count —
	// the reference apps' converge-on-add behavior): the target is the
	// highest of (1) localFurthest, how far the owner has actually read in
	// the local library right now, (2) b.LastChapterRead, the binding's own
	// STORED value from the last sync, and (3) remote.Progress, what the
	// tracker reports right now. b.LastChapterRead MUST stay in the chain as
	// a floor: it is what stops a REGRESSED remote report — or a series with
	// zero locally-read chapters, e.g. immediately after a fresh bind whose
	// only prior state is whatever the remote happened to report at that
	// moment — from dragging a binding DOWN below a value it was already
	// converged to on a previous sync. Dropping it would turn "never
	// regress" into "regress whenever local happens to be behind the last
	// sync," the exact bug this three-way chain exists to prevent.
	converged := kernel.Converge(kernel.Converge(localFurthest, b.LastChapterRead), remote.Progress)
	truncated := float64(kernel.TruncateForInteger(converged))

	upd := applyRemoteFields(b.Update().
		SetLastChapterRead(truncated).
		SetTotalChapters(remote.TotalChapters).
		SetScore(remote.Score).
		SetPrivate(remote.Private), remote)

	s.pushBack(ctx, t, token, b, truncated, remote)

	updated, err := upd.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("syncsvc: persist sync result for binding %s: %w", b.ID, err)
	}
	s.syncSidecar(ctx, updated.SeriesID)

	// Pull-direction convergence (spec §2b): the binding row above is the
	// source of truth for this sync and is already durable, so a mark-read
	// failure is logged, not fatal — it must not undo a successful binding
	// sync, but it also must not be swallowed silently (it hides a real bug
	// otherwise; mirrors syncSidecar's own best-effort posture).
	if markErr := s.markLocalRead(ctx, updated.SeriesID, converged); markErr != nil {
		slog.WarnContext(ctx, "syncsvc: SyncNow: mark-local-read failed",
			"track_binding_id", b.ID, "series_id", updated.SeriesID, "err", markErr)
	}

	return updated, nil
}

// applyRemoteFields sets the optional remote-only fields (native status,
// remote-assigned library id, start/finish dates) on upd whenever the
// fetched entry actually carries them — the SyncNow pull-direction mirror
// of pushOne's applyPushEntryToUpdate.
func applyRemoteFields(upd *ent.TrackBindingUpdateOne, remote *tracker.TrackEntry) *ent.TrackBindingUpdateOne {
	if remote.Status != "" {
		upd = upd.SetStatus(remote.Status)
	}
	if remote.LibraryID != "" {
		upd = upd.SetLibraryID(remote.LibraryID)
	}
	if remote.StartDate != nil {
		upd = upd.SetStartDate(*remote.StartDate)
	}
	if remote.FinishDate != nil {
		upd = upd.SetFinishDate(*remote.FinishDate)
	}
	return upd
}

// pushBack sends the converged value back to t when the local side was
// strictly ahead of the just-fetched remote (Converge only picks the TARGET
// both sides move to; NextPush — reused verbatim from pushOne — is the
// decision for whether THIS side still needs to send it). A push failure
// here does not fail the whole sync — the local row's convergence is still
// correct and durable; the failure is logged and the outstanding remote
// push is enqueued to the retry queue.
//
// The pushed entry is seeded from remote itself (a full copy, Progress
// overridden) — NOT from b, the STALE pre-sync local row — so every other
// field (Score/Private/Status/dates) round-trips back to the tracker
// exactly as it currently holds them. remote is the freshest known truth
// for those fields (just-fetched via GetEntry, and syncOneBinding's own
// applyRemoteFields unconditionally adopts them onto the local row too);
// b's in-memory Score/Private/Status can be stale relative to it (e.g. the
// owner edited them directly on the tracker's own site since the last
// sync), and pushing THOSE back would reintroduce the exact
// clobber-the-remote bug this fix closes, just via a narrower path.
func (s *Service) pushBack(ctx context.Context, t tracker.Tracker, token string, b *ent.TrackBinding, truncated float64, remote *tracker.TrackEntry) {
	push, shouldPush := kernel.NextPush(truncated, remote.Progress)
	if !shouldPush {
		return
	}
	entry := *remote
	entry.Progress = push
	if _, pushErr := t.UpdateEntry(ctx, token, entry); pushErr != nil {
		s.markExpiredOnTokenFailure(ctx, b.TrackerID, pushErr)
		slog.WarnContext(ctx, "syncsvc: SyncNow: push-back failed, enqueueing for retry",
			"track_binding_id", b.ID, "err", pushErr)
		if enqErr := s.retryQueue.Enqueue(ctx, b.ID, push); enqErr != nil {
			slog.WarnContext(ctx, "syncsvc: SyncNow: enqueue after push-back failure also failed",
				"track_binding_id", b.ID, "err", enqErr)
		}
	}
}
