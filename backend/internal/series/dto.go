// Package series holds the library read service: listing and detail of the
// series that M2's ingest populates, with per-series chapter-state rollups.
// The ent predicate package internal/ent/series collides with this package name
// and must be imported aliased (entseries) wherever both are needed.
package series

import (
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
)

// ChapterCounts is the per-series rollup of chapter download state used in list
// and detail responses. Total is every chapter; the other fields count chapters
// currently in that state. (States not broken out here — e.g. downloading,
// upgrading — still contribute to Total.)
type ChapterCounts struct {
	Total      int `json:"total"`
	Downloaded int `json:"downloaded"`
	Wanted     int `json:"wanted"`
	Failed     int `json:"failed"`
}

// SeriesSummaryDTO is the list-row shape for a single series: identity,
// display metadata, the chapter-state rollup, and the monitoring flag.
type SeriesSummaryDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Slug          string        `json:"slug"`
	Category      string        `json:"category"`
	CoverURL      string        `json:"coverUrl"`
	Monitored     bool          `json:"monitored"`
	ChapterCounts ChapterCounts `json:"chapterCounts"`
}

// SeriesDetailDTO is the full series view: the summary fields plus the series'
// chapters (ordered by number then chapter_key), its providers, and the monitoring flag.
type SeriesDetailDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Slug          string        `json:"slug"`
	Category      string        `json:"category"`
	CoverURL      string        `json:"coverUrl"`
	Monitored     bool          `json:"monitored"`
	ChapterCounts ChapterCounts `json:"chapterCounts"`
	Chapters      []ChapterDTO  `json:"chapters"`
	Providers     []ProviderDTO `json:"providers"`
}

// ChapterDTO is one chapter in a series-detail response. Number is the display/
// sort value (nullable — never identity, that is ChapterKey). PageCount is
// nullable until the chapter is downloaded.
type ChapterDTO struct {
	ChapterKey string   `json:"chapterKey"`
	Number     *float64 `json:"number"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Filename   string   `json:"filename"`
	PageCount  *int     `json:"pageCount"`
}

// ProviderDTO is one SeriesProvider in a series-detail response. ID is the
// SeriesProvider UUID (used by Task 5/7 re-rank). Importance is the
// priority/quality rank (higher = preferred). The health fields (Health,
// ChaptersBehind, NewestChapterAt, LastSyncedAt, LastError) are derived on
// read from the source's availability feed and sync state — never persisted.
type ProviderDTO struct {
	ID              string     `json:"id"`
	Provider        string     `json:"provider"`
	Scanlator       string     `json:"scanlator"`
	Language        string     `json:"language"`
	Importance      int        `json:"importance"`
	Health          string     `json:"health"`
	ChaptersBehind  int        `json:"chaptersBehind"`
	NewestChapterAt *time.Time `json:"newestChapterAt"`
	LastSyncedAt    *time.Time `json:"lastSyncedAt"`
	LastError       string     `json:"lastError"`
}

// LibraryHealthDTO is the library-wide source-health scan: only series that
// have at least one stale or erroring source.
type LibraryHealthDTO struct {
	Series []SeriesHealthDTO `json:"series"`
}

// SeriesHealthDTO is one sick series in the library-health scan and its sick
// sources.
type SeriesHealthDTO struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Slug    string        `json:"slug"`
	Sources []ProviderDTO `json:"sources"`
}

// CategoryCountDTO is one row of the /api/categories response: a category enum
// value and the number of series currently filed under it. Every enum value is
// reported, including those with a zero count.
type CategoryCountDTO struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// newSummaryDTO maps an ent.Series plus its computed rollup into a summary DTO.
func newSummaryDTO(s *ent.Series, counts ChapterCounts) SeriesSummaryDTO {
	return SeriesSummaryDTO{
		ID:            s.ID.String(),
		Title:         s.Title,
		Slug:          s.Slug,
		Category:      s.Category.String(),
		CoverURL:      s.CoverURL,
		Monitored:     s.Monitored,
		ChapterCounts: counts,
	}
}

// newChapterDTO maps an ent.Chapter into its detail DTO. The chapter's display
// title lives on the provider feed, not the Chapter row, so name is resolved by
// the caller (best-provider ProviderChapter.name) and passed in; an empty name is
// legitimate when no provider supplies a title.
func newChapterDTO(c *ent.Chapter, name string) ChapterDTO {
	return ChapterDTO{
		ChapterKey: c.ChapterKey,
		Number:     c.Number,
		Name:       name,
		State:      c.State.String(),
		Filename:   c.Filename,
		PageCount:  c.PageCount,
	}
}

// newProviderDTO maps an ent.SeriesProvider and its computed health into a
// detail DTO. h carries all derived health fields; they are never persisted.
func newProviderDTO(p *ent.SeriesProvider, h ProviderHealth) ProviderDTO {
	return ProviderDTO{
		ID:              p.ID.String(),
		Provider:        p.Provider,
		Scanlator:       p.Scanlator,
		Language:        p.Language,
		Importance:      p.Importance,
		Health:          h.Status,
		ChaptersBehind:  h.ChaptersBehind,
		NewestChapterAt: h.NewestChapterAt,
		LastSyncedAt:    h.LastSyncedAt,
		LastError:       h.LastError,
	}
}
