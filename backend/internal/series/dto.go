// Package series holds the library read service: listing and detail of the
// series that M2's ingest populates, with per-series chapter-state rollups.
// The ent predicate package internal/ent/series collides with this package name
// and must be imported aliased (entseries) wherever both are needed.
package series

import "github.com/technobecet/tsundoku/internal/ent"

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
// display metadata, and the chapter-state rollup.
type SeriesSummaryDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Slug          string        `json:"slug"`
	Category      string        `json:"category"`
	CoverURL      string        `json:"coverUrl"`
	ChapterCounts ChapterCounts `json:"chapterCounts"`
}

// SeriesDetailDTO is the full series view: the summary fields plus the series'
// chapters (ordered by number then chapter_key) and its providers.
type SeriesDetailDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Slug          string        `json:"slug"`
	Category      string        `json:"category"`
	CoverURL      string        `json:"coverUrl"`
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

// ProviderDTO is one SeriesProvider in a series-detail response. Importance is
// the priority/quality rank (higher = preferred).
type ProviderDTO struct {
	Provider   string `json:"provider"`
	Scanlator  string `json:"scanlator"`
	Language   string `json:"language"`
	Importance int    `json:"importance"`
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
		ChapterCounts: counts,
	}
}

// newChapterDTO maps an ent.Chapter into its detail DTO. The chapter's display
// title lives on the provider feed, not the Chapter row, so Name is left empty
// here — detail surfaces only the canonical chapter fields.
func newChapterDTO(c *ent.Chapter) ChapterDTO {
	return ChapterDTO{
		ChapterKey: c.ChapterKey,
		Number:     c.Number,
		State:      c.State.String(),
		Filename:   c.Filename,
		PageCount:  c.PageCount,
	}
}

// newProviderDTO maps an ent.SeriesProvider into its detail DTO.
func newProviderDTO(p *ent.SeriesProvider) ProviderDTO {
	return ProviderDTO{
		Provider:   p.Provider,
		Scanlator:  p.Scanlator,
		Language:   p.Language,
		Importance: p.Importance,
	}
}
