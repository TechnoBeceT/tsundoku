// Package downloads is the cross-library chapter-activity domain. Tsundoku has
// no download-queue table — Chapter.state IS the queue — so this package exposes
// the read views over Chapter.state that span every series (Active / Failed /
// Queued screens) plus the owner retry actions that reset failed chapters back
// to wanted. The name + display-title + cover resolution reuses the exported
// resolvers from internal/series (ChapterTitles / MetadataProvider /
// SeriesDisplay) so the importance logic lives in exactly one place (§2 DRY).
package downloads

import (
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
)

// DownloadChapterDTO is one chapter in the cross-library activity list, enriched
// with its series + provider context. JSON is camelCase for the generated TS
// client. SeriesTitle is the resolved display name (M10 two-title model, falls
// back to the canonical title); SeriesCoverURL is the cover proxy path
// ("/api/series/{id}/cover") or "" when no provider supplies a cover. Name is the
// best-provider ProviderChapter.name and is "" when no provider titles the
// chapter (the FE then derives "Chapter {number}"). Provider is the source key
// (SeriesProvider.provider — the raw numeric id) of the source the chapter is
// ACTUALLY coming from — its satisfier, else the highest-importance source whose
// feed carries the key, else "" when no source carries it at all (see
// chapterProvider); ProviderName is that same source's human-readable display
// label (falls back to the id), which the UI shows in place of the id. Both are
// "" for a chapter nothing is fetching — the UI renders that as an em-dash.
// CAVEAT: this is "who supplies this chapter", not a provenance guarantee — a
// DOWNLOADED chapter whose satisfier was cleared (series.RemoveProvider, which
// keeps the CBZ) has no stored provenance left, so it names a remaining feed
// carrier, or nothing at all. Case 2 is also a UI HINT, not engine state: the
// engine excludes retry-exhausted / cooling-down / breaker-tripped sources this
// read model cannot see.
//
// UpgradeTarget is the display label of the source an UPGRADING chapter is
// converging TO (the UI renders "<ProviderName> → <UpgradeTarget>"), and is ""
// for every chapter that is not in upgrade_available / upgrading. Without it the
// row would show only the source being REPLACED, which is exactly the wrong one
// during a convergence wave. It is the INTENDED target, not the engine's
// authoritative pick — see upgradeTargetLabel for the resolution rule and where
// the two can differ.
type DownloadChapterDTO struct {
	ID             uuid.UUID  `json:"id"`
	SeriesID       uuid.UUID  `json:"seriesId"`
	SeriesTitle    string     `json:"seriesTitle"`
	SeriesCategory string     `json:"seriesCategory"`
	SeriesCoverURL string     `json:"seriesCoverUrl"`
	ChapterKey     string     `json:"chapterKey"`
	Number         *float64   `json:"number"`
	Name           string     `json:"name"`
	State          string     `json:"state"`
	Provider       string     `json:"provider"`
	ProviderName   string     `json:"providerName"`
	UpgradeTarget  string     `json:"upgradeTarget"`
	Retries        int        `json:"retries"`
	NextAttemptAt  *time.Time `json:"nextAttemptAt"`
	LastError      string     `json:"lastError"`
	ErrorCategory  string     `json:"errorCategory"`
	Filename       string     `json:"filename"`
	PageCount      *int       `json:"pageCount"`
	DownloadDate   *time.Time `json:"downloadDate"`
}

// DownloadListDTO is the paginated GET /api/downloads response: the total number
// of chapters matching the state filter (across the whole library, not just the
// page) plus the requested page of items.
type DownloadListDTO struct {
	Total int                  `json:"total"`
	Items []DownloadChapterDTO `json:"items"`
}

// RetryAllResultDTO is the POST /api/downloads/retry-all response: the number of
// chapters reset back to wanted by the bulk retry.
type RetryAllResultDTO struct {
	Retried int `json:"retried"`
}

// seriesResolution holds the once-per-series derived values reused across all of
// that series' chapters on a page: the chapter_key→name map, the resolved display
// name + cover proxy path, and the chapter_key→carriers feed index (ordered as the
// engine orders candidates: importance DESC, then ProviderChapter.ID ASC).
//
// upgradeTargets serves BOTH source questions a row asks, from the one index:
// which source an upgrading chapter is converging TO (upgradeTargetLabel), and —
// for a chapter with no satisfier — which source is actually FETCHING it
// (chapterProvider). Both answers are "the highest-importance source whose feed
// carries this key", which is exactly the scheduler's primary-source rule, so they
// share one definition (§2 DRY) and cost no extra query. A chapter no feed carries
// resolves to no source at all ("").
type seriesResolution struct {
	names          map[string]string
	displayName    string
	coverURL       string
	upgradeTargets upgradeTargetIndex
}

// newDownloadChapterDTO maps one Chapter row to its enriched DTO. The series
// context (display name, category, cover, chapter name, provider id + name,
// upgrade target) is resolved once per series by the caller and passed in, so this
// mapper does no lookups — it only projects fields, ensuring every contract field is
// populated (§16).
func newDownloadChapterDTO(ch *ent.Chapter, category string, res seriesResolution, provider, providerName, upgradeTarget string) DownloadChapterDTO {
	return DownloadChapterDTO{
		ID:             ch.ID,
		SeriesID:       ch.SeriesID,
		SeriesTitle:    res.displayName,
		SeriesCategory: category,
		SeriesCoverURL: res.coverURL,
		ChapterKey:     ch.ChapterKey,
		Number:         ch.Number,
		Name:           res.names[ch.ChapterKey],
		State:          ch.State.String(),
		Provider:       provider,
		ProviderName:   providerName,
		UpgradeTarget:  upgradeTarget,
		Retries:        ch.Retries,
		NextAttemptAt:  ch.NextAttemptAt,
		LastError:      ch.LastError,
		ErrorCategory:  ch.ErrorCategory,
		Filename:       ch.Filename,
		PageCount:      ch.PageCount,
		DownloadDate:   ch.DownloadDate,
	}
}
