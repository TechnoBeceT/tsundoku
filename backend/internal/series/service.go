package series

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entlatestseries "github.com/technobecet/tsundoku/internal/ent/latestseries"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	entsuwayomisyncstate "github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
)

// ErrSeriesNotFound is returned by GetSeries when no series matches the given id.
// The HTTP handler maps it to a 404.
var ErrSeriesNotFound = errors.New("series not found")

// ErrProviderNotInSeries is returned by ReorderProviders when a ProviderRank
// references a SeriesProvider that does not belong to the given series. The HTTP
// handler maps it to a 400.
var ErrProviderNotInSeries = errors.New("provider does not belong to series")

// ErrNoCover is returned by CoverURL and ProviderCoverURL when the resolved
// provider has no stored cover_url. The HTTP handler maps it to a 404.
var ErrNoCover = errors.New("no cover available")

// Service is the library read service over the M0 entities. It owns the storage
// root (unused by the read methods; the recategorize path that moves folders on
// disk will use it) so all library operations share one service. staleGrace
// resolves the M7 source-health staleness grace AT USE-TIME so the value can be
// runtime-tuned via the settings overlay without a restart.
type Service struct {
	client  *ent.Client
	storage string

	// sw fetches cover images from Suwayomi on a cold/stale local cover (see
	// cover.go). Optional — attach it with WithCoverFetcher; nil means a cached
	// cover still serves, but a cold one reports ErrCoverFetchFailed.
	sw CoverFetcher

	// progressPusher fires the tracker-sync progress push after a reader
	// marks a chapter read (see tracksync.go). Optional — attach it with
	// WithProgressPusher; nil means SetProgress never fires a tracker push
	// (every pre-existing series/reader test is unaffected).
	progressPusher ProgressPusher

	// sourceLister reports the engine's currently-loaded source ids so the
	// health scan can flag a provider whose extension was uninstalled as
	// HealthUnavailable (see sourcelister.go). Optional — attach it with
	// WithSourceLister; nil means no source is ever flagged unavailable.
	sourceLister SourceLister

	staleGrace func(ctx context.Context) int
}

// NewService builds the series library service with a FIXED stale-grace (the
// common form for tests and any caller that does not need runtime tuning).
// staleGraceDays tunes the M7 source-health staleness rule (see HealthConfig).
func NewService(client *ent.Client, storage string, staleGraceDays int) *Service {
	return NewServiceWithStaleGrace(client, storage, func(context.Context) int { return staleGraceDays })
}

// NewServiceWithStaleGrace builds the series library service whose stale-grace is
// resolved at use-time from staleGrace (e.g. settings.Service.StaleGraceDays), so
// an owner's change via the settings API takes effect on the next health read
// without a restart. Production wires this variant; NewService is the fixed form.
func NewServiceWithStaleGrace(client *ent.Client, storage string, staleGrace func(ctx context.Context) int) *Service {
	return &Service{client: client, storage: storage, staleGrace: staleGrace}
}

// importanceStep is the spacing between adjacent providers on the clean
// importance spread ReorderProviders normalizes to. Higher importance = higher
// priority (see CLAUDE.md "Provider importance — higher number = higher priority").
const importanceStep = 10

// ProviderRank pairs a SeriesProvider UUID with the desired importance value. Used
// by ReorderProviders to update provider priority in a single transaction.
type ProviderRank struct {
	SeriesProviderID uuid.UUID
	Importance       int
}

// normalizeRanks reassigns the given ranks onto a clean, non-negative,
// strictly-descending importance spread: the ranks are ordered by their
// SUBMITTED importance (descending; ties keep their original slice position),
// then each is given importance (n-idx)*importanceStep so the highest-priority
// provider gets the largest value and the lowest gets importanceStep. Only the
// relative ORDER of the submitted importances is honoured — the absolute values
// are canonicalised. This self-heals any legacy negative or duplicated
// importances (e.g. the old below-existing spread that could go negative) into a
// coherent order every time the owner reorders, so the reorder can never fail on
// out-of-range input.
func normalizeRanks(ranks []ProviderRank) []ProviderRank {
	sorted := make([]ProviderRank, len(ranks))
	copy(sorted, ranks)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Importance > sorted[j].Importance
	})
	n := len(sorted)
	out := make([]ProviderRank, n)
	for i, r := range sorted {
		out[i] = ProviderRank{SeriesProviderID: r.SeriesProviderID, Importance: (n - i) * importanceStep}
	}
	return out
}

// SetMonitored updates the monitored flag for the series identified by id.
// A missing id returns ErrSeriesNotFound; the HTTP handler maps it to a 404.
func (s *Service) SetMonitored(ctx context.Context, id uuid.UUID, monitored bool) error {
	err := s.client.Series.UpdateOneID(id).SetMonitored(monitored).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		// Defensive path: non-not-found update errors are only reachable on a DB-level
		// failure (connection dropped / query error) — not forceable in a black-box test.
		return fmt.Errorf("series.SetMonitored: update series %s: %w", id, err)
	}
	return nil
}

// SetCompleted marks a series finished (or re-opens it). A completed series is
// skipped by the refresh sweep and excluded from source-health. The flag is
// reversible (completed=false resumes polling, e.g. a surprise new season).
// A missing id yields ErrSeriesNotFound.
func (s *Service) SetCompleted(ctx context.Context, id uuid.UUID, completed bool) error {
	err := s.client.Series.UpdateOneID(id).SetCompleted(completed).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		// Defensive path: non-not-found update errors are only reachable on a DB-level
		// failure (connection dropped / query error) — not forceable in a black-box test.
		return fmt.Errorf("series.SetCompleted: update series %s: %w", id, err)
	}
	return nil
}

// ReorderProviders updates the importance values for a set of SeriesProviders in
// a single all-or-nothing transaction.
//
// The submitted importances express only the desired ORDER: they are normalized
// (normalizeRanks) to a clean non-negative descending spread before persisting,
// so a submitted negative importance (legacy bad data from the old below-existing
// spread) is tolerated and self-healed rather than rejected. Only the relative
// order of the submitted importances is honoured — the persisted values are
// canonicalised.
//
// This ONLY PERSISTS importance — the upgrade re-evaluation that consumes the new
// ranking is the next download ticker cycle. This method does NOT trigger any
// re-evaluation or upgrade logic.
//
// Error semantics:
//   - id not found → ErrSeriesNotFound (whole tx rolled back).
//   - any rank's SeriesProviderID does not belong to id → ErrProviderNotInSeries
//     (whole tx rolled back; importances are ALL-OR-NOTHING — no partial update).
func (s *Service) ReorderProviders(ctx context.Context, id uuid.UUID, ranks []ProviderRank) error {
	ranks = normalizeRanks(ranks)

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("series.ReorderProviders: begin tx: %w", err)
	}

	if err := reorderProvidersInTx(ctx, tx, id, ranks); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("series.ReorderProviders: commit tx: %w", err)
	}
	return nil
}

// reorderProvidersInTx is the transactional body of ReorderProviders. It confirms
// the series exists, validates provider ownership for every rank, then applies all
// importance updates. A single ownership failure rolls back the entire set.
func reorderProvidersInTx(ctx context.Context, tx *ent.Tx, id uuid.UUID, ranks []ProviderRank) error {
	// Confirm the series exists before touching any provider rows.
	exists, err := tx.Series.Query().Where(entseries.IDEQ(id)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.ReorderProviders: check series %s: %w", id, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}

	for _, r := range ranks {
		// Verify the provider exists AND belongs to this series.
		owned, err := tx.SeriesProvider.Query().
			Where(
				entseriesprovider.IDEQ(r.SeriesProviderID),
				entseriesprovider.SeriesID(id),
			).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("series.ReorderProviders: check provider %s: %w", r.SeriesProviderID, err)
		}
		if !owned {
			return ErrProviderNotInSeries
		}

		if err := tx.SeriesProvider.UpdateOneID(r.SeriesProviderID).SetImportance(r.Importance).Exec(ctx); err != nil {
			// Defensive path: the Exist check above confirmed the row exists and
			// belongs to this series; an error here is only reachable on a
			// concurrent delete or a DB-level failure — not forceable in a
			// black-box test without tearing down the shared transaction.
			return fmt.Errorf("series.ReorderProviders: update importance for provider %s: %w", r.SeriesProviderID, err)
		}
	}
	return nil
}

// RemoveProvider removes one source (SeriesProvider) from a series in a single
// all-or-nothing transaction: it clears the satisfied_by edge on any chapters
// that source satisfied (keeping satisfied_importance as a quality watermark),
// deletes the source's ProviderChapter availability feed and its
// SuwayomiSyncState, then deletes the SeriesProvider row. It performs NO disk
// I/O — every downloaded CBZ and every Chapter row is preserved (M6
// keep-CBZs invariant). Removing the last source is allowed and leaves a
// 0-provider series in place. Returns ErrSeriesNotFound if id is unknown, or
// ErrProviderNotInSeries if providerID does not belong to the series.
func (s *Service) RemoveProvider(ctx context.Context, id, providerID uuid.UUID) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("series.RemoveProvider: begin tx: %w", err)
	}
	if err := removeProviderInTx(ctx, tx, id, providerID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("series.RemoveProvider: commit tx: %w", err)
	}
	return nil
}

// removeProviderInTx performs the FK-safe ordered deletion. Order matters: the
// SeriesProvider row can only be deleted after every row that references it
// (ProviderChapter, SuwayomiSyncState) is gone and the Chapter.satisfied_by FK
// pointing at it is cleared — there is no DB-level cascade (deliberately, since
// a cascade would destroy downloaded Chapter rows).
func removeProviderInTx(ctx context.Context, tx *ent.Tx, id, providerID uuid.UUID) error {
	exists, err := tx.Series.Query().Where(entseries.IDEQ(id)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.RemoveProvider: check series %s: %w", id, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}

	owned, err := tx.SeriesProvider.Query().
		Where(entseriesprovider.IDEQ(providerID), entseriesprovider.SeriesID(id)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.RemoveProvider: check provider %s: %w", providerID, err)
	}
	if !owned {
		return ErrProviderNotInSeries
	}

	// Dangling-pointer guard: if the series' metadata_provider_id currently points
	// at the provider being removed, clear it so the pointer never dangles after
	// the row is gone. The predicate update is a no-op (0 rows) when the pointer
	// is absent or points elsewhere — not an error.
	if err := tx.Series.Update().
		Where(entseries.IDEQ(id), entseries.MetadataProviderIDEQ(providerID)).
		ClearMetadataProviderID().
		Exec(ctx); err != nil {
		return fmt.Errorf("series.RemoveProvider: clear dangling metadata_provider_id for %s: %w", providerID, err)
	}

	// 1. Clear satisfied_by on chapters this source satisfied — keep the
	//    satisfied_importance watermark (do NOT call ClearSatisfiedImportance).
	if err := tx.Chapter.Update().
		Where(entchapter.SatisfiedByProviderID(providerID)).
		ClearSatisfiedBy().
		Exec(ctx); err != nil {
		return fmt.Errorf("series.RemoveProvider: clear satisfied_by for provider %s: %w", providerID, err)
	}

	// 2. Delete the source's availability feed.
	if _, err := tx.ProviderChapter.Delete().
		Where(entproviderchapter.SeriesProviderID(providerID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("series.RemoveProvider: delete provider chapters for %s: %w", providerID, err)
	}

	// 3. Delete its sync state (0 or 1 row).
	if _, err := tx.SuwayomiSyncState.Delete().
		Where(entsuwayomisyncstate.SeriesProviderID(providerID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("series.RemoveProvider: delete sync state for %s: %w", providerID, err)
	}

	// 4. Delete the source row itself.
	if err := tx.SeriesProvider.DeleteOneID(providerID).Exec(ctx); err != nil {
		return fmt.Errorf("series.RemoveProvider: delete provider %s: %w", providerID, err)
	}
	return nil
}

// DeleteSeries permanently removes a whole series. It always deletes every DB
// row for the series (the full cascade); it removes the series' downloaded CBZ
// files + library folder from disk ONLY when deleteFiles is true. With
// deleteFiles=false the files are left on disk (only DB tracking is removed; a
// later disk.Reconcile can rebuild the series from the on-disk sidecar). This is
// the 2nd sanctioned owner-initiated deletion path (after RemoveProvider) and the
// first that can delete a CBZ — there is still no automatic deletion. A missing
// id yields ErrSeriesNotFound.
//
// Disk and DB stay consistent: the folder removal (deleteFiles=true) runs before
// the tx commits, and the tx is rolled back if removal fails, so a disk error
// leaves the DB fully intact (retryable) and a success leaves no orphan folder
// for a later reconcile to resurrect.
func (s *Service) DeleteSeries(ctx context.Context, id uuid.UUID, deleteFiles bool) error {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		return fmt.Errorf("series.DeleteSeries: load series %s: %w", id, err)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("series.DeleteSeries: begin tx: %w", err)
	}
	if err := deleteSeriesInTx(ctx, tx, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if deleteFiles {
		removed, err := disk.RemoveSeriesDir(s.storage, category.NameOf(row), row.Title)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("series.DeleteSeries: %w", err)
		}
		if !removed {
			slog.Warn("series.DeleteSeries: deleteFiles requested but no series folder found — nothing deleted on disk (the on-disk title may have drifted from the DB title)",
				"series_id", row.ID, "title", row.Title, "category", category.NameOf(row))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("series.DeleteSeries: commit tx: %w", err)
	}
	return nil
}

// deleteSeriesInTx removes every DB row owned by a series, FK-safe (children
// before parents): the providers' chapter feeds + sync states, then the chapters
// (whose satisfied_by references a provider), then the providers, then the
// edge-less LatestSeries row, then the series itself.
func deleteSeriesInTx(ctx context.Context, tx *ent.Tx, id uuid.UUID) error {
	providerIDs, err := tx.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(id)).IDs(ctx)
	if err != nil {
		return fmt.Errorf("series.DeleteSeries: list providers for %s: %w", id, err)
	}

	if _, err := tx.ProviderChapter.Delete().
		Where(entproviderchapter.SeriesProviderIDIn(providerIDs...)).Exec(ctx); err != nil {
		return fmt.Errorf("series.DeleteSeries: delete provider chapters for %s: %w", id, err)
	}
	if _, err := tx.SuwayomiSyncState.Delete().
		Where(entsuwayomisyncstate.SeriesProviderIDIn(providerIDs...)).Exec(ctx); err != nil {
		return fmt.Errorf("series.DeleteSeries: delete sync states for %s: %w", id, err)
	}
	if _, err := tx.Chapter.Delete().
		Where(entchapter.SeriesID(id)).Exec(ctx); err != nil {
		return fmt.Errorf("series.DeleteSeries: delete chapters for %s: %w", id, err)
	}
	if _, err := tx.SeriesProvider.Delete().
		Where(entseriesprovider.SeriesID(id)).Exec(ctx); err != nil {
		return fmt.Errorf("series.DeleteSeries: delete providers for %s: %w", id, err)
	}
	if _, err := tx.LatestSeries.Delete().
		Where(entlatestseries.SeriesID(id)).Exec(ctx); err != nil {
		return fmt.Errorf("series.DeleteSeries: delete latest-series for %s: %w", id, err)
	}
	if err := tx.Series.DeleteOneID(id).Exec(ctx); err != nil {
		return fmt.Errorf("series.DeleteSeries: delete series %s: %w", id, err)
	}
	return nil
}

// ListFilter selects and paginates a ListSeries call. Category, when set,
// restricts the result to that enum value. Limit (when > 0) caps the page size;
// Offset skips that many rows. Results are always ordered by title ascending so
// pagination is deterministic.
type ListFilter struct {
	Category *string
	Limit    int
	Offset   int
}

// ListSeries returns a title-ASC page of series summaries. The per-series
// chapter-state rollup is computed with a SINGLE grouped aggregate query
// (GROUP BY series_id, state) over only the page's series ids — not one query
// per series — so list cost stays constant in the number of series. Providers
// are eagerly loaded in a single secondary query (no N+1) so DisplayName and
// CoverURL can be resolved from the metadata source provider without extra
// round-trips.
func (s *Service) ListSeries(ctx context.Context, filter ListFilter) ([]SeriesSummaryDTO, error) {
	q := s.client.Series.Query().Order(entseries.ByTitle()).WithProviders().WithCategory()

	if filter.Category != nil {
		// Filter by category NAME via the edge. An unknown name simply matches
		// no series (an empty page) — categories are now user-defined, so there is
		// no fixed enum to validate against.
		q = q.Where(entseries.HasCategoryWith(entcategory.Name(*filter.Category)))
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}

	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("series.ListSeries: query series: %w", err)
	}

	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}

	rollups, err := s.chapterRollups(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]SeriesSummaryDTO, len(rows))
	for i, r := range rows {
		out[i] = newSummaryDTO(r, rollups[r.ID])
	}
	return out, nil
}

// CountSeries returns the total number of series matching the filter's category
// (ignoring Limit/Offset) — used for the GET /api/series X-Total-Count header.
func (s *Service) CountSeries(ctx context.Context, filter ListFilter) (int, error) {
	q := s.client.Series.Query()
	if filter.Category != nil {
		q = q.Where(entseries.HasCategoryWith(entcategory.Name(*filter.Category)))
	}
	return q.Count(ctx)
}

// GetSeries returns the full detail of one series: its summary fields, the
// chapter-state rollup, its chapters (ordered by number then chapter_key), and
// its providers. Each chapter's display Name is sourced from the best provider's
// ProviderChapter title (see ChapterTitles). A missing id yields ErrSeriesNotFound.
func (s *Service) GetSeries(ctx context.Context, id uuid.UUID) (SeriesDetailDTO, error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithChapters(func(cq *ent.ChapterQuery) {
			cq.Order(entchapter.ByNumber(), entchapter.ByChapterKey())
		}).
		// Eager-load providers WITH their per-chapter feed and sync state so
		// chapter titles and source health can be resolved without an extra query
		// per provider (no N+1): one nested load over the already-loaded providers
		// supplies every ProviderChapter row and each provider's SyncState.
		WithProviders(func(pq *ent.SeriesProviderQuery) {
			pq.WithProviderChapters().WithSyncState()
		}).
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return SeriesDetailDTO{}, ErrSeriesNotFound
		}
		return SeriesDetailDTO{}, fmt.Errorf("series.GetSeries: query series %s: %w", id, err)
	}

	titles := ChapterTitles(row.Edges.Providers)

	chapters := make([]ChapterDTO, len(row.Edges.Chapters))
	counts := ChapterCounts{}
	var lastDownloaded *time.Time // MAX(first_downloaded_at) over non-superseded chapters
	for i, ch := range row.Edges.Chapters {
		chapters[i] = newChapterDTO(ch, titles[ch.ChapterKey])
		if ch.State == entchapter.StateSuperseded || ch.State == entchapter.StateIgnored {
			// superseded parts are merged into their whole; ignored fractionals are
			// suppressed re-uploads — neither is counted (mirrors chapterRollups) and
			// the FE hides both from the chapter list.
			continue
		}
		counts.Total++
		addToCounts(&counts, ch)
		lastDownloaded = laterTime(lastDownloaded, ch.FirstDownloadedAt)
	}

	metaProv := MetadataProvider(row)
	dispName, coverURL := SeriesDisplay(row, metaProv)

	keys, maxNumber, multi := seriesHealthInputs(row)
	now := time.Now().UTC()
	grace := s.staleGrace(ctx)             // read at use-time (hot-reloadable)
	loaded, active := s.loadedSources(ctx) // one engine call for the whole detail (fail-safe)
	chapterCounts := providerChapterCounts(row)
	providers := make([]ProviderDTO, len(row.Edges.Providers))
	for i, p := range row.Edges.Providers {
		isMetaSrc := metaProv != nil && p.ID == metaProv.ID
		unavailable := active && providerSourceUnavailable(p, loaded)
		providers[i] = newProviderDTO(p, s.providerHealth(p, keys, maxNumber, multi, row.Completed, unavailable, now, grace), row.ID, isMetaSrc, chapterCounts[p.ID])
	}

	return SeriesDetailDTO{
		ID:                      row.ID.String(),
		Title:                   row.Title,
		DisplayName:             dispName,
		Slug:                    row.Slug,
		Category:                category.NameOf(row),
		CoverURL:                coverURL,
		Monitored:               row.Monitored,
		Completed:               row.Completed,
		NeedsSource:             needsSource(row.Edges.Providers),
		ChapterCounts:           counts,
		CreatedAt:               formatRFC3339(row.CreatedAt),
		LastChapterDownloadedAt: formatRFC3339Ptr(lastDownloaded),
		Chapters:                chapters,
		Providers:               providers,

		Status:         row.Status,
		Description:    row.Description,
		Genres:         nonNilStrings(row.Genres),
		Tags:           nonNilStrings(row.Tags),
		AltTitles:      mapAltTitles(row.AltTitles),
		Authors:        mapAuthors(row.Authors),
		Links:          sourceLinks(row.Edges.Providers, mapLinks(row.Links)),
		Year:           row.Year,
		MetadataSource: mapSourceRef(row.MetadataSource),
		CoverSource:    mapSourceRef(row.CoverSource),
		MetadataLocked: row.MetadataLocked,
	}, nil
}

// seriesHealthInputs derives the shared inputs (key set + leading-edge number +
// multi-source flag) used to compute every provider's health for one series.
func seriesHealthInputs(row *ent.Series) (keys map[string]struct{}, maxNumber *float64, multi bool) {
	keys = make(map[string]struct{}, len(row.Edges.Chapters))
	for _, ch := range row.Edges.Chapters {
		keys[ch.ChapterKey] = struct{}{}
		if ch.Number != nil && (maxNumber == nil || *ch.Number > *maxNumber) {
			n := *ch.Number
			maxNumber = &n
		}
	}
	return keys, maxNumber, len(row.Edges.Providers) > 1
}

// providerHealth computes one provider's health within an already-loaded series.
// completed is the series' completed flag — when true the source is reported ok
// (excluded from staleness/erroring). unavailable is true when this live
// provider's source is no longer loaded in the engine (its extension was
// uninstalled) — resolved once per scan by the caller. grace is the resolved
// (hot-reloadable) stale-grace, passed in by the caller after one
// s.staleGrace(ctx) read.
func (s *Service) providerHealth(p *ent.SeriesProvider, keys map[string]struct{}, maxNumber *float64, multi, completed, unavailable bool, now time.Time, grace int) ProviderHealth {
	return ComputeProviderHealth(ProviderHealthInput{
		SyncState:         p.Edges.SyncState,
		ProviderChapters:  p.Edges.ProviderChapters,
		SeriesChapterKeys: keys,
		SeriesMaxNumber:   maxNumber,
		MultiSource:       multi,
		Completed:         completed,
		SourceUnavailable: unavailable,
	}, now, grace)
}

// loadSeriesWithHealthData loads every series with the chapters, providers,
// provider-chapters and sync-state needed to compute source health.
func (s *Service) loadSeriesWithHealthData(ctx context.Context) ([]*ent.Series, error) {
	rows, err := s.client.Series.Query().
		Order(entseries.ByTitle()).
		WithChapters().
		WithProviders(func(pq *ent.SeriesProviderQuery) {
			pq.WithProviderChapters().WithSyncState()
		}).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("series.loadSeriesWithHealthData: %w", err)
	}
	return rows, nil
}

// sickSources returns the providers of one loaded series whose health is stale,
// erroring, or unavailable (empty if the series is fully healthy). grace is the
// resolved stale-grace, read once by the caller. loaded/active are the resolved
// engine-source set (active=false ⇒ nothing flagged unavailable — fail-safe),
// also resolved once per scan by the caller (LibraryHealth / UnhealthyCount).
func (s *Service) sickSources(row *ent.Series, now time.Time, grace int, loaded map[int64]struct{}, active bool) []ProviderDTO {
	keys, maxNumber, multi := seriesHealthInputs(row)
	metaProv := MetadataProvider(row)
	chapterCounts := providerChapterCounts(row)
	var sick []ProviderDTO
	for _, p := range row.Edges.Providers {
		unavailable := active && providerSourceUnavailable(p, loaded)
		h := s.providerHealth(p, keys, maxNumber, multi, row.Completed, unavailable, now, grace)
		if h.Status == HealthStale || h.Status == HealthErroring || h.Status == HealthUnavailable {
			isMetaSrc := metaProv != nil && p.ID == metaProv.ID
			sick = append(sick, newProviderDTO(p, h, row.ID, isMetaSrc, chapterCounts[p.ID]))
		}
	}
	return sick
}

// LibraryHealth returns only the series that have at least one stale, erroring,
// or unavailable source, each with its sick sources listed. The engine's
// loaded-source set is resolved ONCE for the whole scan (no N+1).
func (s *Service) LibraryHealth(ctx context.Context) (LibraryHealthDTO, error) {
	rows, err := s.loadSeriesWithHealthData(ctx)
	if err != nil {
		return LibraryHealthDTO{}, err
	}
	now := time.Now().UTC()
	grace := s.staleGrace(ctx)             // read once at use-time (hot-reloadable)
	loaded, active := s.loadedSources(ctx) // one engine call for the whole scan (fail-safe)
	out := LibraryHealthDTO{Series: []SeriesHealthDTO{}}
	for _, row := range rows {
		if sick := s.sickSources(row, now, grace, loaded, active); len(sick) > 0 {
			out.Series = append(out.Series, SeriesHealthDTO{
				ID: row.ID.String(), Title: row.Title, Slug: row.Slug, Sources: sick,
			})
		}
	}
	return out, nil
}

// UnhealthyCount is the number of series with at least one stale/erroring/
// unavailable source — the cheap figure behind the health.summary SSE. The
// engine's loaded-source set is resolved ONCE for the whole scan (no N+1).
func (s *Service) UnhealthyCount(ctx context.Context) (int, error) {
	rows, err := s.loadSeriesWithHealthData(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	grace := s.staleGrace(ctx)             // read once at use-time (hot-reloadable)
	loaded, active := s.loadedSources(ctx) // one engine call for the whole scan (fail-safe)
	n := 0
	for _, row := range rows {
		if len(s.sickSources(row, now, grace, loaded, active)) > 0 {
			n++
		}
	}
	return n, nil
}

// SetCategory recategorizes a series to the category identified by categoryID,
// keeping the DB and disk consistent.
//
// It resolves the target category (missing → category.ErrCategoryNotFound), loads
// the series for its current category + title (missing → ErrSeriesNotFound), and:
//   - if the target equals the current category → a no-op, returns nil.
//   - otherwise moves the series folder on disk FIRST (by the two category
//     NAMES), then updates the DB FK, with compensation, so DB and disk never end
//     in disagreement (either both old, both new, or a surfaced error):
//   - disk.MoveSeriesCategory relocates <storage>/<old>/<title> to
//     <storage>/<new>/<title> and rewrites the sidecar.
//   - on a successful move the DB category_id is updated; if that DB update fails
//     the folder is moved back (compensation) and the DB error is returned
//     (joined with any compensation failure so nothing is swallowed).
//
// No-disk-folder branch: a not-yet-downloaded series has no folder on disk yet,
// so there is nothing to move. We detect this by stat-ing the source dir and
// skipping the move only when it genuinely does not exist (os.IsNotExist). Any
// other move failure (collision, cross-device, permission) is NOT treated as
// "no folder" — the folder exists, so MoveSeriesCategory runs and its error
// propagates. This keeps the DB-only path strictly limited to series with no
// rendered chapters.
func (s *Service) SetCategory(ctx context.Context, id, categoryID uuid.UUID) error {
	target, err := s.client.Category.Get(ctx, categoryID)
	if err != nil {
		if ent.IsNotFound(err) {
			return category.ErrCategoryNotFound
		}
		return fmt.Errorf("series.SetCategory: load category %s: %w", categoryID, err)
	}

	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		// Defensive path: a non-not-found load error is reachable only on a DB-level
		// failure (connection dropped / query error) — not forceable in a black-box
		// test without tearing down the shared client.
		return fmt.Errorf("series.SetCategory: load series %s: %w", id, err)
	}

	if row.CategoryID == categoryID {
		return nil
	}
	currentName := category.NameOf(row)

	moved, err := s.moveSeriesFolder(currentName, target.Name, row.Title)
	if err != nil {
		return err
	}

	// Defensive path (the whole DB-failure + compensation block below): reachable
	// only when the DB UPDATE fails AFTER the disk move/skip already succeeded.
	// Forcing it in a black-box test would require injecting a mid-operation DB
	// failure, which the standard says to document rather than wire a production
	// seam for. The compensation logic itself is exercised in reverse by the happy
	// move test (it is the same MoveSeriesCategory call with swapped categories).
	if err := s.client.Series.UpdateOneID(id).SetCategoryID(categoryID).Exec(ctx); err != nil {
		dbErr := fmt.Errorf("series.SetCategory: update DB category for %s: %w", id, err)
		if !moved {
			return dbErr
		}
		// Compensate: the folder already moved but the DB update failed. Move it
		// back so disk matches the still-old DB state. If the compensation also
		// fails, surface BOTH errors — never swallow either (§16).
		if cErr := disk.MoveSeriesCategory(s.storage, target.Name, currentName, row.Title); cErr != nil {
			return errors.Join(dbErr, fmt.Errorf("series.SetCategory: compensating move-back failed: %w", cErr))
		}
		return dbErr
	}

	return nil
}

// moveSeriesFolder moves the series folder on disk from oldCat to newCat, unless
// the series has no folder yet (not-yet-downloaded). It returns moved=true when a
// real move happened (so SetCategory knows whether to compensate on a later DB
// failure), moved=false when the move was skipped because the source dir is
// genuinely absent. A real move failure is returned as-is and never masked as
// "no folder".
func (s *Service) moveSeriesFolder(oldCat, newCat, title string) (moved bool, err error) {
	src := disk.SeriesDir(s.storage, oldCat, title)
	if _, statErr := os.Stat(src); statErr != nil {
		if os.IsNotExist(statErr) {
			// No-disk-folder branch: nothing rendered yet, DB-only update.
			return false, nil
		}
		// Defensive path: reachable only on an OS-level stat failure other than
		// not-exist (permission denied / fd exhausted). Surfaced, not swallowed.
		return false, fmt.Errorf("series.SetCategory: stat series dir %q: %w", src, statErr)
	}

	if err := disk.MoveSeriesCategory(s.storage, oldCat, newCat, title); err != nil {
		return false, fmt.Errorf("series.SetCategory: move folder: %w", err)
	}
	return true, nil
}

// chapterRollupRow is the scan target for the grouped chapter-count aggregate.
// Read is grouped alongside State so the SAME query can also tally Unread
// (downloaded AND read=false) without a second round-trip or a per-series loop.
type chapterRollupRow struct {
	SeriesID uuid.UUID        `json:"series_id"`
	State    entchapter.State `json:"state"`
	Read     bool             `json:"read"`
	Count    int              `json:"count"`
	// MaxFirstDownloadedAt is the newest first_downloaded_at within this
	// (state, read) group. NULLABLE (*time.Time): a group may hold only chapters
	// that never carried one (e.g. wanted chapters), in which case MAX is SQL NULL
	// → nil. The caller folds the per-group maxima into ONE per-series maximum,
	// ignoring nils.
	MaxFirstDownloadedAt *time.Time `json:"max"`
}

// seriesRollup is the per-series result of chapterRollups: the chapter-state
// counts plus the newest first_downloaded_at across all the series' chapters
// (nil when no chapter ever carried one). LastChapterDownloadedAt is deliberately
// MAX(first_downloaded_at), NOT MAX(download_date) — see the DTO doc.
type seriesRollup struct {
	Counts                  ChapterCounts
	LastChapterDownloadedAt *time.Time
}

// chapterRollups runs ONE grouped aggregate (GROUP BY series_id, state, read)
// over the given series ids and returns a per-series seriesRollup map — the
// chapter-state counts AND MAX(first_downloaded_at), both from the SAME query
// (no N+1, no second round-trip: the nullable MAX folds into the existing
// GroupBy aggregate). Returns an empty map (not nil) when there are no ids, so
// callers can index it safely.
func (s *Service) chapterRollups(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]seriesRollup, error) {
	out := make(map[uuid.UUID]seriesRollup, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	var rows []chapterRollupRow
	err := s.client.Chapter.Query().
		Where(entchapter.SeriesIDIn(ids...)).
		GroupBy(entchapter.FieldSeriesID, entchapter.FieldState, entchapter.FieldRead).
		Aggregate(ent.Count(), ent.As(ent.Max(entchapter.FieldFirstDownloadedAt), "max")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("series.chapterRollups: aggregate chapter states: %w", err)
	}

	for _, r := range rows {
		if r.State == entchapter.StateSuperseded || r.State == entchapter.StateIgnored {
			continue // superseded parts merged into their whole; ignored fractionals suppressed — neither counted
		}
		agg := out[r.SeriesID]
		agg.Counts.Total += r.Count
		switch r.State {
		case entchapter.StateDownloaded:
			agg.Counts.Downloaded += r.Count
			if !r.Read {
				agg.Counts.Unread += r.Count
			}
		case entchapter.StateWanted:
			agg.Counts.Wanted += r.Count
		case entchapter.StateFailed:
			agg.Counts.Failed += r.Count
		}
		agg.LastChapterDownloadedAt = laterTime(agg.LastChapterDownloadedAt, r.MaxFirstDownloadedAt)
		out[r.SeriesID] = agg
	}
	return out, nil
}

// laterTime returns the newer of two nullable timestamps (nil = absent). It is
// how chapterRollups folds each group's MAX(first_downloaded_at) into one
// per-series maximum without a superseded group ever contributing (those rows
// are skipped before this is called).
func laterTime(a, b *time.Time) *time.Time {
	switch {
	case b == nil:
		return a
	case a == nil:
		return b
	case b.After(*a):
		return b
	default:
		return a
	}
}

// MetadataProvider returns the SeriesProvider that supplies the display metadata
// (title + cover) for a series. It honours the explicit metadata_provider_id pin
// when set and the pointed-to provider is present in the loaded Providers slice;
// otherwise it falls back to the highest-importance provider; otherwise nil.
// row.Edges.Providers must be eagerly loaded by the caller. Exported so the
// downloads domain can resolve a series' display name + cover without
// duplicating the pin-then-importance resolution logic (§2 DRY).
func MetadataProvider(row *ent.Series) *ent.SeriesProvider {
	if row.MetadataProviderID != nil {
		for _, p := range row.Edges.Providers {
			if p.ID == *row.MetadataProviderID {
				return p
			}
		}
		// Pin is set but provider not in edges (removed): fall through to auto.
	}
	return HighestImportanceProvider(row.Edges.Providers)
}

// HighestImportanceProvider returns the SeriesProvider with the greatest
// importance value (higher = preferred — the project-wide convention), or nil
// when the slice is empty. It is the automatic metadata-source fallback and the
// best-available source for the downloads list's provider field; exported so
// both resolve "the series' top source" through one definition (§2 DRY).
func HighestImportanceProvider(providers []*ent.SeriesProvider) *ent.SeriesProvider {
	var best *ent.SeriesProvider
	for _, p := range providers {
		if best == nil || p.Importance > best.Importance {
			best = p
		}
	}
	return best
}

// ProviderLabel returns the human-readable source label for a SeriesProvider:
// its provider_name (the display name captured at ingest, e.g. "WebToon") when
// non-empty, else provider (the raw numeric Suwayomi source-ID identity key) as
// a fallback. Exported so the series-detail and downloads DTOs both resolve the
// display-vs-id fallback through one definition (§2 DRY) — a numeric ID is never
// shown when a name is available, and the label is never empty for a real row.
func ProviderLabel(p *ent.SeriesProvider) string {
	if p.ProviderName != "" {
		return p.ProviderName
	}
	return p.Provider
}

// SeriesDisplay derives the display name and cover proxy URL for a series.
// name is metaProv.Title when non-empty, else row.Title (canonical fallback).
// coverURL is the series cover proxy path — see seriesCoverURL for the exact
// resolution rule (a locally-cached cover surfaces even without a provider).
// Exported so the downloads domain reuses the identical name+cover resolution.
//
// Reading it is FREE: cover_version + cover_url are columns already loaded with
// the row (metaProv is one of row.Edges.Providers), so building a DTO does zero
// disk I/O.
func SeriesDisplay(row *ent.Series, metaProv *ent.SeriesProvider) (name, coverURL string) {
	name = row.Title
	if metaProv != nil && metaProv.Title != "" {
		name = metaProv.Title
	}
	return name, seriesCoverURL(row, metaProv)
}

// seriesCoverURL resolves the series cover proxy path, cached-cover-first:
//
//  1. A LOCALLY-CACHED cover (row.CoverVersion != "" ⇒ cover_file is populated)
//     is servable regardless of whether a Suwayomi provider still supplies a
//     cover_url. This is the metadata-engine cover path: SetCover / AutoIdentify
//     persist cover_file + cover_version onto a providerless (or disk-only)
//     series — a Kaizoku-migration series with no online source — and its cover
//     must both SURFACE on the card and SERVE. The URL carries the CONTENT
//     version ("…/cover?v=<cover_version>", a hash of the BYTES), so it changes
//     exactly when the image does and the endpoint can answer `immutable`.
//  2. No local cover, but a provider supplies a cover_url ⇒ the M10 provider
//     path: emit the UNVERSIONED proxy path. The endpoint cold-fetches from the
//     source on first request and serves revalidatable no-cache, so an uncached
//     cover can never be pinned.
//  3. Neither ⇒ "" (the endpoint would have nothing to serve).
//
// Step 1 sitting ABOVE step 2 is deliberate and behaviour-preserving: a
// provider-cover series that HAS warmed its cache already had cover_version set,
// so it kept emitting the versioned URL before this change too — the only new
// behaviour is that a cached cover WITHOUT a provider cover_url now surfaces
// instead of resolving to "".
func seriesCoverURL(row *ent.Series, metaProv *ent.SeriesProvider) string {
	if row.CoverVersion != "" {
		return "/api/series/" + row.ID.String() + "/cover?v=" + row.CoverVersion
	}
	if metaProv != nil && metaProv.CoverURL != "" {
		return "/api/series/" + row.ID.String() + "/cover"
	}
	return ""
}

// SetMetadataSource pins the series' metadata source to the given provider
// (providerID non-nil) or resets to automatic resolution (providerID nil).
// When providerID is set it must belong to the series (ErrProviderNotInSeries);
// a missing series id returns ErrSeriesNotFound.
func (s *Service) SetMetadataSource(ctx context.Context, id uuid.UUID, providerID *uuid.UUID) error {
	exists, err := s.client.Series.Query().Where(entseries.IDEQ(id)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.SetMetadataSource: check series %s: %w", id, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}
	upd := s.client.Series.UpdateOneID(id)
	if providerID == nil {
		upd = upd.ClearMetadataProviderID()
	} else {
		owned, err := s.client.SeriesProvider.Query().
			Where(entseriesprovider.IDEQ(*providerID), entseriesprovider.SeriesID(id)).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("series.SetMetadataSource: check provider %s: %w", *providerID, err)
		}
		if !owned {
			return ErrProviderNotInSeries
		}
		upd = upd.SetMetadataProviderID(*providerID)
	}
	if err := upd.Exec(ctx); err != nil {
		// Defensive path: the series exists (confirmed above) and the provider is
		// owned (confirmed above); an error here is reachable only on a DB-level
		// failure — not forceable in a black-box test.
		return fmt.Errorf("series.SetMetadataSource: update %s: %w", id, err)
	}
	return nil
}

// SetIgnoreFractional flags one of the series' sources as a fractional
// re-uploader (ignore=true), or clears the flag (ignore=false). A flagged source
// stops contributing fractional-numbered chapters (5.1, 5.5 …) to THIS series:
// they are dropped at ingest and excluded from download candidacy. Its WHOLE
// chapters are unaffected — the toggle suppresses a mirror's re-uploads, it does
// not disable the source.
//
// It is per (series, provider) and an explicit OWNER decision, never a heuristic:
// the engine cannot tell a re-upload from a genuine side-chapter (a ".5" omake
// source obviously also hosts the whole chapter), so the owner ticks it after
// SEEING that source's fractional list (ProviderDTO.fractionalChapters).
//
// It DELETES NOTHING (never-auto-delete): the fractional ProviderChapter rows
// already ingested from that source, every Chapter row, and every CBZ already
// downloaded from it are all kept. Un-ticking restores the source immediately.
// Cleaning up already-downloaded duplicates stays a separate, explicit owner
// action (DedupeFiles).
//
// An unknown series returns ErrSeriesNotFound (→404); a providerID that does not
// belong to this series returns ErrProviderNotInSeries (→400). A one-column
// update needs no transaction — the two existence checks are the whole contract.
func (s *Service) SetIgnoreFractional(ctx context.Context, id, providerID uuid.UUID, ignore bool) error {
	exists, err := s.client.Series.Query().Where(entseries.IDEQ(id)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.SetIgnoreFractional: check series %s: %w", id, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}

	owned, err := s.client.SeriesProvider.Query().
		Where(entseriesprovider.IDEQ(providerID), entseriesprovider.SeriesID(id)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.SetIgnoreFractional: check provider %s: %w", providerID, err)
	}
	if !owned {
		return ErrProviderNotInSeries
	}

	if err := s.client.SeriesProvider.UpdateOneID(providerID).SetIgnoreFractional(ignore).Exec(ctx); err != nil {
		// Defensive path: the provider row's existence was just confirmed above, so
		// this is reachable only on a DB-level failure — not forceable in a
		// black-box test.
		return fmt.Errorf("series.SetIgnoreFractional: update provider %s: %w", providerID, err)
	}

	// Reconcile the series' UNDOWNLOADED fractionals against the new flags: park a
	// wanted/failed fractional whose every carrier now ignores fractionals into the
	// terminal `ignored` state (out of the queue and the chapter list), and restore
	// an ignored one that just regained a non-ignoring carrier back to wanted. This
	// is a state change only (never-auto-delete); already-downloaded fractionals are
	// untouched (their cleanup stays the explicit DedupeFiles action).
	if err := s.reconcileIgnoredFractionals(ctx, id); err != nil {
		return fmt.Errorf("series.SetIgnoreFractional: reconcile ignored fractionals for series %s: %w", id, err)
	}
	return nil
}

// ChapterTitles builds a chapter_key → display title map from the series'
// eagerly-loaded providers and their ProviderChapter feeds. For each chapter_key
// it picks the name from the provider with the HIGHEST importance that supplies a
// non-empty name (mirroring M1's best-provider rule: higher importance = higher
// priority). An empty name never shadows a real one from a lower-importance
// provider; a key no provider titles is simply absent (legitimately empty Name,
// not a dropped field). Cost is one pass over the already-loaded rows — no N+1.
// Exported so the downloads list resolves chapter names through this one
// best-provider definition (§2 DRY).
func ChapterTitles(providers []*ent.SeriesProvider) map[string]string {
	titles := make(map[string]string)
	bestImportance := make(map[string]int)
	for _, p := range providers {
		for _, pc := range p.Edges.ProviderChapters {
			if pc.Name == "" {
				continue
			}
			if cur, seen := bestImportance[pc.ChapterKey]; !seen || p.Importance > cur {
				titles[pc.ChapterKey] = pc.Name
				bestImportance[pc.ChapterKey] = p.Importance
			}
		}
	}
	return titles
}

// addToCounts increments the rollup for a single chapter. Total is tallied by
// the caller; this bumps the broken-out per-state counters plus Unread
// (downloaded AND read=false — mirrors chapterRollups' grouped tally).
func addToCounts(c *ChapterCounts, ch *ent.Chapter) {
	switch ch.State {
	case entchapter.StateDownloaded:
		c.Downloaded++
		if !ch.Read {
			c.Unread++
		}
	case entchapter.StateWanted:
		c.Wanted++
	case entchapter.StateFailed:
		c.Failed++
	}
}

// CoverURL returns the stored Suwayomi-relative cover_url of the series'
// resolved metadata provider. Returns ErrSeriesNotFound when id is unknown,
// ErrNoCover when the metadata provider has no stored cover (the proxy
// endpoint would have nothing to fetch). Providers must be eagerly loaded for
// MetadataProvider resolution.
func (s *Service) CoverURL(ctx context.Context, id uuid.UUID) (string, error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithProviders().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrSeriesNotFound
		}
		return "", fmt.Errorf("series.CoverURL: load series %s: %w", id, err)
	}
	meta := MetadataProvider(row)
	if meta == nil || meta.CoverURL == "" {
		return "", ErrNoCover
	}
	return meta.CoverURL, nil
}

// ProviderCoverURL returns the stored cover_url for the given SeriesProvider
// AND the numeric engine source id (ProviderSourceID) to fetch it from — the
// per-provider cover proxy (handler/series.ProviderCover) needs both to call
// sourceengine.Client.Image(ctx, sourceID, "", coverURL). Returns
// ErrSeriesNotFound when the series is unknown, ErrProviderNotInSeries when
// providerID does not belong to the series, ErrNoCover when the provider has
// no stored cover_url, and ErrCoverFetchFailed when the provider is
// disk-origin (Provider is a display NAME, not a numeric engine source id) —
// there is no source to fetch from, the same failure mode a live fetch error
// produces.
func (s *Service) ProviderCoverURL(ctx context.Context, id, providerID uuid.UUID) (coverURL string, sourceID int64, err error) {
	exists, err := s.client.Series.Query().Where(entseries.IDEQ(id)).Exist(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("series.ProviderCoverURL: check series %s: %w", id, err)
	}
	if !exists {
		return "", 0, ErrSeriesNotFound
	}
	p, err := s.client.SeriesProvider.Query().
		Where(entseriesprovider.IDEQ(providerID), entseriesprovider.SeriesID(id)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", 0, ErrProviderNotInSeries
		}
		return "", 0, fmt.Errorf("series.ProviderCoverURL: load provider %s: %w", providerID, err)
	}
	if p.CoverURL == "" {
		return "", 0, ErrNoCover
	}
	sid, ok := ProviderSourceID(p)
	if !ok {
		return "", 0, fmt.Errorf("%w: series %s: provider %s is disk-origin, no engine source to fetch its cover from",
			ErrCoverFetchFailed, id, p.ID)
	}
	return p.CoverURL, sid, nil
}
