package series

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// ErrSeriesNotFound is returned by GetSeries when no series matches the given id.
// The HTTP handler maps it to a 404.
var ErrSeriesNotFound = errors.New("series not found")

// ErrInvalidCategory is returned by SetCategory when the requested category is not
// one of the legal Series.category enum values. The HTTP handler maps it to a 400.
var ErrInvalidCategory = errors.New("invalid category")

// Service is the library read service over the M0 entities. It owns the storage
// root (unused by the read methods; the recategorize path that moves folders on
// disk will use it) so all library operations share one service.
type Service struct {
	client  *ent.Client
	storage string
}

// NewService constructs a Service bound to an Ent client and the storage root.
func NewService(client *ent.Client, storage string) *Service {
	return &Service{client: client, storage: storage}
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
// per series — so list cost stays constant in the number of series.
func (s *Service) ListSeries(ctx context.Context, filter ListFilter) ([]SeriesSummaryDTO, error) {
	q := s.client.Series.Query().Order(entseries.ByTitle())

	if filter.Category != nil {
		q = q.Where(entseries.CategoryEQ(entseries.Category(*filter.Category)))
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
// its providers. A missing id yields ErrSeriesNotFound.
func (s *Service) GetSeries(ctx context.Context, id uuid.UUID) (SeriesDetailDTO, error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithChapters(func(cq *ent.ChapterQuery) {
			cq.Order(entchapter.ByNumber(), entchapter.ByChapterKey())
		}).
		WithProviders().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return SeriesDetailDTO{}, ErrSeriesNotFound
		}
		return SeriesDetailDTO{}, fmt.Errorf("series.GetSeries: query series %s: %w", id, err)
	}

	chapters := make([]ChapterDTO, len(row.Edges.Chapters))
	counts := ChapterCounts{Total: len(row.Edges.Chapters)}
	for i, ch := range row.Edges.Chapters {
		chapters[i] = newChapterDTO(ch)
		addToCounts(&counts, ch.State)
	}

	providers := make([]ProviderDTO, len(row.Edges.Providers))
	for i, p := range row.Edges.Providers {
		providers[i] = newProviderDTO(p)
	}

	return SeriesDetailDTO{
		ID:            row.ID.String(),
		Title:         row.Title,
		Slug:          row.Slug,
		Category:      row.Category.String(),
		CoverURL:      row.CoverURL,
		ChapterCounts: counts,
		Chapters:      chapters,
		Providers:     providers,
	}, nil
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
