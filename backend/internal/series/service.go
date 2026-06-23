package series

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// ErrSeriesNotFound is returned by GetSeries when no series matches the given id.
// The HTTP handler maps it to a 404.
var ErrSeriesNotFound = errors.New("series not found")

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
