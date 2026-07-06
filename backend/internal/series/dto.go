// Package series holds the library read service: listing and detail of the
// series that M2's ingest populates, with per-series chapter-state rollups.
// The ent predicate package internal/ent/series collides with this package name
// and must be imported aliased (entseries) wherever both are needed.
package series

import (
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
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
// DisplayName is the resolved display title from the metadata source provider
// (falls back to the canonical Series.title). CoverURL is the series cover proxy
// path ("/api/series/{id}/cover"), empty when no provider has a cover_url.
type SeriesSummaryDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	DisplayName   string        `json:"displayName"`
	Slug          string        `json:"slug"`
	Category      string        `json:"category"`
	CoverURL      string        `json:"coverUrl"`
	Monitored     bool          `json:"monitored"`
	Completed     bool          `json:"completed"`
	ChapterCounts ChapterCounts `json:"chapterCounts"`
}

// SeriesDetailDTO is the full series view: the summary fields plus the series'
// chapters (ordered by number then chapter_key), its providers, and the
// monitoring flag. DisplayName and CoverURL follow the same resolution as
// SeriesSummaryDTO.
type SeriesDetailDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	DisplayName   string        `json:"displayName"`
	Slug          string        `json:"slug"`
	Category      string        `json:"category"`
	CoverURL      string        `json:"coverUrl"`
	Monitored     bool          `json:"monitored"`
	Completed     bool          `json:"completed"`
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
// SeriesProvider UUID (used by re-rank). Provider is the raw Suwayomi source-ID
// identity key; ProviderName is its human-readable display label (falls back to
// the id when no name was captured) — the UI shows ProviderName, keeps Provider
// for identity. Importance is the priority/quality rank
// (higher = preferred). Title is this provider's own title for the series.
// CoverURL is the provider-level cover proxy path
// ("/api/series/{sid}/providers/{pid}/cover"). IsMetadataSource is true for the
// resolved metadata provider (the one currently supplying DisplayName + CoverURL).
// The health fields (Health, ChaptersBehind, NewestChapterAt, LastSyncedAt,
// LastError) are derived on read — never persisted. Linked is false for a
// disk-origin provider (suwayomi_id == 0 — an "unlinked/unknown group" created
// by library import/reconcile, never a real Suwayomi source) so the FE can list
// it as a Match candidate. MangaID is the source's Suwayomi manga ID; 0 for
// unlinked disk-origin providers. ChapterCount is how many of the series' chapters
// this provider currently satisfies (Chapter.satisfied_by_provider_id == this
// provider) — the coverage the owner sees before choosing what to match it to.
type ProviderDTO struct {
	ID               string     `json:"id"`
	Provider         string     `json:"provider"`
	ProviderName     string     `json:"providerName"`
	Title            string     `json:"title"`
	CoverURL         string     `json:"coverUrl"`
	IsMetadataSource bool       `json:"isMetadataSource"`
	Linked           bool       `json:"linked"`
	MangaID          int        `json:"mangaId"`
	ChapterCount     int        `json:"chapterCount"`
	Scanlator        string     `json:"scanlator"`
	Language         string     `json:"language"`
	Importance       int        `json:"importance"`
	Health           string     `json:"health"`
	ChaptersBehind   int        `json:"chaptersBehind"`
	NewestChapterAt  *time.Time `json:"newestChapterAt"`
	LastSyncedAt     *time.Time `json:"lastSyncedAt"`
	LastError        string     `json:"lastError"`
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

// newSummaryDTO maps an ent.Series plus its computed rollup into a summary DTO.
// s.Edges.Providers AND s.Edges.Category must be eagerly loaded; MetadataProvider
// + SeriesDisplay resolve DisplayName and CoverURL from the provider set, and
// category.NameOf resolves the category name from the edge.
func newSummaryDTO(s *ent.Series, counts ChapterCounts) SeriesSummaryDTO {
	meta := MetadataProvider(s)
	dispName, coverURL := SeriesDisplay(s, meta)
	return SeriesSummaryDTO{
		ID:            s.ID.String(),
		Title:         s.Title,
		DisplayName:   dispName,
		Slug:          s.Slug,
		Category:      category.NameOf(s),
		CoverURL:      coverURL,
		Monitored:     s.Monitored,
		Completed:     s.Completed,
		ChapterCounts: counts,
	}
}

// newChapterDTO maps an ent.Chapter into its detail DTO. The chapter's display
// title lives on the provider feed, not the Chapter row, so the resolved name
// (best-provider ProviderChapter.name) is passed in by the caller. When no
// provider supplies a title we fall back to "Chapter N" derived from the chapter
// number — a frozen 0-provider series (all sources removed via M6) keeps its CBZs
// and Chapter rows but loses the title source, so the number is the only display
// name left. If even the number is absent (a rare corner) the name stays blank.
func newChapterDTO(c *ent.Chapter, name string) ChapterDTO {
	return ChapterDTO{
		ChapterKey: c.ChapterKey,
		Number:     c.Number,
		Name:       chapterDisplayName(name, c.Number),
		State:      c.State.String(),
		Filename:   c.Filename,
		PageCount:  c.PageCount,
	}
}

// chapterDisplayName returns the chapter's display name: the provider-resolved
// title if present, else "Chapter N" from number (minimally formatted so 12.0 →
// "Chapter 12" and 12.5 → "Chapter 12.5"), else "" when there is no number.
func chapterDisplayName(name string, number *float64) string {
	if name != "" {
		return name
	}
	if number != nil {
		return "Chapter " + strconv.FormatFloat(*number, 'f', -1, 64)
	}
	return ""
}

// newProviderDTO maps an ent.SeriesProvider and its computed health into a
// detail DTO. seriesID and isMetadataSource are passed in by the caller after
// resolving the series' metadata provider once for the whole provider slice.
// CoverURL is the provider-level proxy path when the provider has a non-empty
// cover_url, else "" (mirroring the series-level SeriesDisplay behaviour so the
// SPA never fires a cover fetch that would 404). Title is the provider's own
// title for the series (set at ingest, may be "").
func newProviderDTO(p *ent.SeriesProvider, h ProviderHealth, seriesID uuid.UUID, isMetadataSource bool, chapterCount int) ProviderDTO {
	var coverURL string
	if p.CoverURL != "" {
		coverURL = "/api/series/" + seriesID.String() + "/providers/" + p.ID.String() + "/cover"
	}
	return ProviderDTO{
		ID:               p.ID.String(),
		Provider:         p.Provider,
		ProviderName:     ProviderLabel(p),
		Title:            p.Title,
		CoverURL:         coverURL,
		IsMetadataSource: isMetadataSource,
		Linked:           p.SuwayomiID != 0,
		MangaID:          p.SuwayomiID,
		ChapterCount:     chapterCount,
		Scanlator:        p.Scanlator,
		Language:         p.Language,
		Importance:       p.Importance,
		Health:           h.Status,
		ChaptersBehind:   h.ChaptersBehind,
		NewestChapterAt:  h.NewestChapterAt,
		LastSyncedAt:     h.LastSyncedAt,
		LastError:        h.LastError,
	}
}

// providerChapterCounts tallies, for one loaded series, how many chapters each
// provider currently satisfies (Chapter.satisfied_by_provider_id). row must
// have its Chapters edge eagerly loaded (GetSeries / loadSeriesWithHealthData
// both do) — this is an in-memory rollup, no extra query (no N+1).
func providerChapterCounts(row *ent.Series) map[uuid.UUID]int {
	counts := make(map[uuid.UUID]int, len(row.Edges.Providers))
	for _, ch := range row.Edges.Chapters {
		if ch.SatisfiedByProviderID == nil {
			continue
		}
		counts[*ch.SatisfiedByProviderID]++
	}
	return counts
}
