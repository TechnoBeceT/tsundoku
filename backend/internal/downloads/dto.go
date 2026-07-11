// Package downloads is the cross-library chapter-activity domain. Tsundoku has
// no download-queue table — Chapter.state IS the queue — so this package exposes
// the read views over Chapter.state that span every series (Active / Failed /
// Queued screens) plus the owner retry actions that reset failed chapters back
// to wanted. The name + display-title + cover resolution reuses the exported
// resolvers from internal/series (ChapterTitles / MetadataProvider /
// SeriesDisplay / HighestImportanceProvider) so the importance logic lives in
// exactly one place (§2 DRY).
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
// (SeriesProvider.provider — the raw numeric id) of the satisfying source, else
// the series' top source; ProviderName is that same source's human-readable
// display label (falls back to the id), which the UI shows in place of the id.
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
// name + cover proxy path, the top source (the fallback for a chapter's provider
// fields when it has no satisfying source yet), and the chapter_key→providers index
// that names an upgrading chapter's target. bestProvider is nil for a 0-provider
// series, in which case a chapter's provider id + name are both "".
type seriesResolution struct {
	names          map[string]string
	displayName    string
	coverURL       string
	bestProvider   *ent.SeriesProvider
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
