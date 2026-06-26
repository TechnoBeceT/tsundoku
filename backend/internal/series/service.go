package series

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
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

// ErrInvalidCategory is returned by SetCategory and ListSeries when the requested
// category is not one of the legal Series.category enum values. The HTTP handler
// maps it to a 400.
var ErrInvalidCategory = errors.New("invalid category")

// ErrProviderNotInSeries is returned by ReorderProviders when a ProviderRank
// references a SeriesProvider that does not belong to the given series. The HTTP
// handler maps it to a 400.
var ErrProviderNotInSeries = errors.New("provider does not belong to series")

// Service is the library read service over the M0 entities. It owns the storage
// root (unused by the read methods; the recategorize path that moves folders on
// disk will use it) so all library operations share one service.
type Service struct {
	client         *ent.Client
	storage        string
	staleGraceDays int
}

// NewService builds the series library service. staleGraceDays tunes the M7
// source-health staleness rule (see HealthConfig).
func NewService(client *ent.Client, storage string, staleGraceDays int) *Service {
	return &Service{client: client, storage: storage, staleGraceDays: staleGraceDays}
}

// ProviderRank pairs a SeriesProvider UUID with the desired importance value. Used
// by ReorderProviders to update provider priority in a single transaction.
type ProviderRank struct {
	SeriesProviderID uuid.UUID
	Importance       int
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
// M4 ONLY PERSISTS importance — the upgrade re-evaluation that consumes the new
// ranking is M5 / the next download ticker cycle. This method does NOT trigger
// any re-evaluation or upgrade logic.
//
// Error semantics:
//   - id not found → ErrSeriesNotFound (whole tx rolled back).
//   - any rank's SeriesProviderID does not belong to id → ErrProviderNotInSeries
//     (whole tx rolled back; importances are ALL-OR-NOTHING — no partial update).
func (s *Service) ReorderProviders(ctx context.Context, id uuid.UUID, ranks []ProviderRank) error {
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
	row, err := s.client.Series.Get(ctx, id)
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
		if err := disk.RemoveSeriesDir(s.storage, row.Category.String(), row.Title); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("series.DeleteSeries: %w", err)
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
	q := s.client.Series.Query().Order(entseries.ByTitle()).WithProviders()

	if filter.Category != nil {
		cat := entseries.Category(*filter.Category)
		// Reject an unknown category instead of silently returning an empty page
		// (an invalid filter applied as a predicate would just match nothing).
		if err := entseries.CategoryValidator(cat); err != nil {
			return nil, fmt.Errorf("series.ListSeries: %q: %w", *filter.Category, ErrInvalidCategory)
		}
		q = q.Where(entseries.CategoryEQ(cat))
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

// GetSeries returns the full detail of one series: its summary fields, the
// chapter-state rollup, its chapters (ordered by number then chapter_key), and
// its providers. Each chapter's display Name is sourced from the best provider's
// ProviderChapter title (see chapterTitles). A missing id yields ErrSeriesNotFound.
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
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return SeriesDetailDTO{}, ErrSeriesNotFound
		}
		return SeriesDetailDTO{}, fmt.Errorf("series.GetSeries: query series %s: %w", id, err)
	}

	titles := chapterTitles(row.Edges.Providers)

	chapters := make([]ChapterDTO, len(row.Edges.Chapters))
	counts := ChapterCounts{Total: len(row.Edges.Chapters)}
	for i, ch := range row.Edges.Chapters {
		chapters[i] = newChapterDTO(ch, titles[ch.ChapterKey])
		addToCounts(&counts, ch.State)
	}

	metaProv := metadataProvider(row)
	dispName, coverURL := seriesDisplay(row, metaProv)

	keys, maxNumber, multi := seriesHealthInputs(row)
	now := time.Now().UTC()
	providers := make([]ProviderDTO, len(row.Edges.Providers))
	for i, p := range row.Edges.Providers {
		isMetaSrc := metaProv != nil && p.ID == metaProv.ID
		providers[i] = newProviderDTO(p, s.providerHealth(p, keys, maxNumber, multi, row.Completed, now), row.ID, isMetaSrc)
	}

	return SeriesDetailDTO{
		ID:            row.ID.String(),
		Title:         row.Title,
		DisplayName:   dispName,
		Slug:          row.Slug,
		Category:      row.Category.String(),
		CoverURL:      coverURL,
		Monitored:     row.Monitored,
		Completed:     row.Completed,
		ChapterCounts: counts,
		Chapters:      chapters,
		Providers:     providers,
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
// (excluded from staleness/erroring).
func (s *Service) providerHealth(p *ent.SeriesProvider, keys map[string]struct{}, maxNumber *float64, multi bool, completed bool, now time.Time) ProviderHealth {
	return ComputeProviderHealth(ProviderHealthInput{
		SyncState:         p.Edges.SyncState,
		ProviderChapters:  p.Edges.ProviderChapters,
		SeriesChapterKeys: keys,
		SeriesMaxNumber:   maxNumber,
		MultiSource:       multi,
		Completed:         completed,
	}, now, s.staleGraceDays)
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

// sickSources returns the providers of one loaded series whose health is stale
// or erroring (empty if the series is fully healthy).
func (s *Service) sickSources(row *ent.Series, now time.Time) []ProviderDTO {
	keys, maxNumber, multi := seriesHealthInputs(row)
	metaProv := metadataProvider(row)
	var sick []ProviderDTO
	for _, p := range row.Edges.Providers {
		h := s.providerHealth(p, keys, maxNumber, multi, row.Completed, now)
		if h.Status == HealthStale || h.Status == HealthErroring {
			isMetaSrc := metaProv != nil && p.ID == metaProv.ID
			sick = append(sick, newProviderDTO(p, h, row.ID, isMetaSrc))
		}
	}
	return sick
}

// LibraryHealth returns only the series that have at least one stale or
// erroring source, each with its sick sources listed.
func (s *Service) LibraryHealth(ctx context.Context) (LibraryHealthDTO, error) {
	rows, err := s.loadSeriesWithHealthData(ctx)
	if err != nil {
		return LibraryHealthDTO{}, err
	}
	now := time.Now().UTC()
	out := LibraryHealthDTO{Series: []SeriesHealthDTO{}}
	for _, row := range rows {
		if sick := s.sickSources(row, now); len(sick) > 0 {
			out.Series = append(out.Series, SeriesHealthDTO{
				ID: row.ID.String(), Title: row.Title, Slug: row.Slug, Sources: sick,
			})
		}
	}
	return out, nil
}

// UnhealthyCount is the number of series with at least one stale/erroring
// source — the cheap figure behind the health.summary SSE.
func (s *Service) UnhealthyCount(ctx context.Context) (int, error) {
	rows, err := s.loadSeriesWithHealthData(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	n := 0
	for _, row := range rows {
		if len(s.sickSources(row, now)) > 0 {
			n++
		}
	}
	return n, nil
}

// SetCategory recategorizes a series, keeping the DB and disk consistent.
//
// It validates newCat is a legal enum value (else ErrInvalidCategory), loads the
// series for its current category + title (missing → ErrSeriesNotFound), and:
//   - if newCat == the current category → a no-op, returns nil.
//   - otherwise moves the series folder on disk FIRST, then updates the DB, with
//     compensation, so DB and disk never end in disagreement (either both old,
//     both new, or a surfaced error):
//   - disk.MoveSeriesCategory relocates <storage>/<old>/<title> to
//     <storage>/<new>/<title> and rewrites the sidecar.
//   - on a successful move the DB category is updated; if that DB update fails
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
func (s *Service) SetCategory(ctx context.Context, id uuid.UUID, newCat string) error {
	cat := entseries.Category(newCat)
	if err := entseries.CategoryValidator(cat); err != nil {
		return fmt.Errorf("series.SetCategory: %q: %w", newCat, ErrInvalidCategory)
	}

	row, err := s.client.Series.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		// Defensive path: a non-not-found load error is reachable only on a DB-level
		// failure (connection dropped / query error) — not forceable in a black-box
		// test without tearing down the shared client.
		return fmt.Errorf("series.SetCategory: load series %s: %w", id, err)
	}

	current := row.Category
	if cat == current {
		return nil
	}

	moved, err := s.moveSeriesFolder(string(current), newCat, row.Title)
	if err != nil {
		return err
	}

	// Defensive path (the whole DB-failure + compensation block below): reachable
	// only when the DB UPDATE fails AFTER the disk move/skip already succeeded.
	// Forcing it in a black-box test would require injecting a mid-operation DB
	// failure, which the standard says to document rather than wire a production
	// seam for. The compensation logic itself is exercised in reverse by the happy
	// move test (it is the same MoveSeriesCategory call with swapped categories).
	if err := s.client.Series.UpdateOneID(id).SetCategory(cat).Exec(ctx); err != nil {
		dbErr := fmt.Errorf("series.SetCategory: update DB category for %s: %w", id, err)
		if !moved {
			return dbErr
		}
		// Compensate: the folder already moved but the DB update failed. Move it
		// back so disk matches the still-old DB state. If the compensation also
		// fails, surface BOTH errors — never swallow either (§16).
		if cErr := disk.MoveSeriesCategory(s.storage, newCat, string(current), row.Title); cErr != nil {
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

// Categories returns one CategoryCountDTO per Series.category enum value — all
// five, including zero-count categories — in the enum's declared order. The
// counts come from a SINGLE grouped aggregate (GROUP BY category); enum values
// with no series are then filled in with a zero count so the response is complete
// and deterministic.
func (s *Service) Categories(ctx context.Context) ([]CategoryCountDTO, error) {
	var rows []struct {
		Category entseries.Category `json:"category"`
		Count    int                `json:"count"`
	}
	err := s.client.Series.Query().
		GroupBy(entseries.FieldCategory).
		Aggregate(ent.Count()).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("series.Categories: aggregate series by category: %w", err)
	}

	counts := make(map[entseries.Category]int, len(rows))
	for _, r := range rows {
		counts[r.Category] = r.Count
	}

	// Declared enum order — deterministic, matches the schema definition.
	order := []entseries.Category{
		entseries.CategoryManga,
		entseries.CategoryManhwa,
		entseries.CategoryManhua,
		entseries.CategoryComic,
		entseries.CategoryOther,
	}
	out := make([]CategoryCountDTO, len(order))
	for i, c := range order {
		out[i] = CategoryCountDTO{Category: string(c), Count: counts[c]}
	}
	return out, nil
}

// chapterRollupRow is the scan target for the grouped chapter-count aggregate.
type chapterRollupRow struct {
	SeriesID uuid.UUID        `json:"series_id"`
	State    entchapter.State `json:"state"`
	Count    int              `json:"count"`
}

// chapterRollups runs ONE grouped aggregate (GROUP BY series_id, state) over the
// given series ids and returns a per-series ChapterCounts map. Returns an empty
// map (not nil) when there are no ids, so callers can index it safely.
func (s *Service) chapterRollups(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]ChapterCounts, error) {
	out := make(map[uuid.UUID]ChapterCounts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	var rows []chapterRollupRow
	err := s.client.Chapter.Query().
		Where(entchapter.SeriesIDIn(ids...)).
		GroupBy(entchapter.FieldSeriesID, entchapter.FieldState).
		Aggregate(ent.Count()).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("series.chapterRollups: aggregate chapter states: %w", err)
	}

	for _, r := range rows {
		c := out[r.SeriesID]
		c.Total += r.Count
		switch r.State {
		case entchapter.StateDownloaded:
			c.Downloaded += r.Count
		case entchapter.StateWanted:
			c.Wanted += r.Count
		case entchapter.StateFailed:
			c.Failed += r.Count
		}
		out[r.SeriesID] = c
	}
	return out, nil
}

// metadataProvider returns the SeriesProvider that supplies the display metadata
// (title + cover) for a series. It honours the explicit metadata_provider_id pin
// when set and the pointed-to provider is present in the loaded Providers slice;
// otherwise it falls back to the highest-importance provider; otherwise nil.
// row.Edges.Providers must be eagerly loaded by the caller.
func metadataProvider(row *ent.Series) *ent.SeriesProvider {
	if row.MetadataProviderID != nil {
		for _, p := range row.Edges.Providers {
			if p.ID == *row.MetadataProviderID {
				return p
			}
		}
		// Pin is set but provider not in edges (removed): fall through to auto.
	}
	var best *ent.SeriesProvider
	for _, p := range row.Edges.Providers {
		if best == nil || p.Importance > best.Importance {
			best = p
		}
	}
	return best
}

// seriesDisplay derives the display name and cover proxy URL for a series.
// name is metaProv.Title when non-empty, else row.Title (canonical fallback).
// coverURL is the series cover proxy path when metaProv has a non-empty
// cover_url, else "" (the proxy endpoint would have nothing to serve).
func seriesDisplay(row *ent.Series, metaProv *ent.SeriesProvider) (name, coverURL string) {
	name = row.Title
	if metaProv != nil && metaProv.Title != "" {
		name = metaProv.Title
	}
	if metaProv != nil && metaProv.CoverURL != "" {
		coverURL = "/api/series/" + row.ID.String() + "/cover"
	}
	return name, coverURL
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

// chapterTitles builds a chapter_key → display title map from the series'
// eagerly-loaded providers and their ProviderChapter feeds. For each chapter_key
// it picks the name from the provider with the HIGHEST importance that supplies a
// non-empty name (mirroring M1's best-provider rule: higher importance = higher
// priority). An empty name never shadows a real one from a lower-importance
// provider; a key no provider titles is simply absent (legitimately empty Name,
// not a dropped field). Cost is one pass over the already-loaded rows — no N+1.
func chapterTitles(providers []*ent.SeriesProvider) map[string]string {
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

// addToCounts increments the rollup for a single chapter's state. Total is
// tallied by the caller; this only bumps the broken-out per-state counters.
func addToCounts(c *ChapterCounts, state entchapter.State) {
	switch state {
	case entchapter.StateDownloaded:
		c.Downloaded++
	case entchapter.StateWanted:
		c.Wanted++
	case entchapter.StateFailed:
		c.Failed++
	}
}
