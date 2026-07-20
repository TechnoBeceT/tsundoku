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
// chapterSource); ProviderName is that same source's human-readable display
// label (falls back to the id), which the UI shows in place of the id. Both are
// "" for a chapter nothing is fetching — the UI renders that as an em-dash.
// CAVEAT: this is "who supplies this chapter", not a provenance guarantee — a
// DOWNLOADED chapter whose satisfier was cleared (series.RemoveProvider, which
// keeps the CBZ) has no stored provenance left, so it names a remaining feed
// carrier, or nothing at all. Case 2 is also a UI HINT, not engine state: the
// engine excludes retry-exhausted / cooling-down / breaker-tripped sources this
// read model cannot see.
//
// Attempts + MaxRetries drive the "N/max" retry badge (the UI renders
// "<ProviderName> · <Attempts>/<MaxRetries>"): Attempts is the resolved source's
// per-source ProviderChapter.attempts (how many times THIS source has failed THIS
// chapter — the download engine's per-source budget, NOT the legacy Chapter.retries),
// and MaxRetries is the current jobs.max_retries budget resolved at request time
// (hot-reloadable; 0 when no retry-settings port is attached). A source is exhausted
// for the chapter at Attempts >= MaxRetries; a chapter fails only when ALL its sources
// are exhausted. Attempts describes the SAME source ProviderName names.
//
// IsUpgrade marks a row as a convergence UPGRADE (state upgrade_available / upgrading)
// rather than a fresh download — the explicit fresh-vs-upgrade discriminator the queue
// UI needs. It is NOT equivalent to "UpgradeTarget != \"\"": a chapter can be
// upgrade_available yet have no nameable target (the only higher source has a feed gap,
// or every candidate is filtered), so IsUpgrade is true while UpgradeTarget is "".
//
// UpgradeTarget is the display label of the source an UPGRADING chapter is
// converging TO (the UI renders "<ProviderName> → <UpgradeTarget>"), and is ""
// for every chapter that is not in upgrade_available / upgrading. Without it the
// row would show only the source being REPLACED, which is exactly the wrong one
// during a convergence wave. It is the INTENDED target, not the engine's
// authoritative pick — see upgradeTargetLabel for the resolution rule and where
// the two can differ.
//
// WaitingReason + DeferredUntil + DeferReason answer WHY a queued chapter is not
// moving. They read the source the engine is waiting on (the upgrade TARGET for an
// upgrading chapter, else the PRIMARY candidate for a wanted one — see waitedOnCarrier)
// and unify its TWO cooldown signals (see waitingStatus):
//   - WaitingReason "backoff": that source has a persisted per-source next_attempt_at
//     in the future (a failed download/upgrade fetch's per-chapter backoff).
//   - WaitingReason "cooling_down": that source's circuit-breaker is tripped
//     (SourceCircuitState.cooldown_until in the future) — the WHOLE source is in
//     anti-ban cooldown. This is the batch-joined breaker read that CLOSES the former
//     KNOWN GAP where a chapter held back purely by the breaker (a different table
//     from next_attempt_at) showed no reason. The snapshot is loaded once per List.
//   - WaitingReason "": the source is ready to try next cycle (never mislabelled).
//
// DeferredUntil is the EFFECTIVE next-eligible time — the LATER of the two timestamps
// when both apply (the engine waits out both), else the single one; populated ONLY
// when in the FUTURE. DeferReason is the binding constraint's recorded message (the
// breaker's last_error for cooling_down, the feed row's last_error for backoff). All
// three are the zero value for a row that is not waiting. The waited-on source's NAME
// is already on the row (UpgradeTarget for an upgrade defer, ProviderName for a wanted
// one), so it is not duplicated here.
type DownloadChapterDTO struct {
	ID             uuid.UUID `json:"id"`
	SeriesID       uuid.UUID `json:"seriesId"`
	SeriesTitle    string    `json:"seriesTitle"`
	SeriesCategory string    `json:"seriesCategory"`
	SeriesCoverURL string    `json:"seriesCoverUrl"`
	ChapterKey     string    `json:"chapterKey"`
	Number         *float64  `json:"number"`
	Name           string    `json:"name"`
	State          string    `json:"state"`
	Provider       string    `json:"provider"`
	ProviderName   string    `json:"providerName"`
	Attempts       int       `json:"attempts"`
	MaxRetries     int       `json:"maxRetries"`
	IsUpgrade      bool      `json:"isUpgrade"`
	UpgradeTarget  string    `json:"upgradeTarget"`

	// FailingProvider / FailingProviderName / FailingAttempts / FailingLastError /
	// FailingErrorCategory describe the FAILING source (the honest fail list, PART D):
	// the source with a chapter-specific per-source failure (ProviderChapter.attempts>0)
	// for this chapter — the highest-importance such carrier (see failingCarrier). They
	// are populated for ANY chapter with such a source REGARDLESS of the chapter's own
	// state (a DOWNLOADED chapter whose UPGRADE source keeps failing is surfaced here,
	// tagged IsUpgrade with the target = FailingProviderName), and are the zero value
	// when no source has failed this chapter chapter-specifically. This is DISTINCT from
	// Provider/ProviderName (the source actually SUPPLYING the chapter): for a downloaded
	// chapter the supplier is its satisfier while the failing source is a broken upgrade
	// target. FailingErrorCategory is derived from FailingLastError via the shared
	// errorclass kernel (ProviderChapter carries no category column).
	FailingProvider      string `json:"failingProvider"`
	FailingProviderName  string `json:"failingProviderName"`
	FailingAttempts      int    `json:"failingAttempts"`
	FailingLastError     string `json:"failingLastError"`
	FailingErrorCategory string `json:"failingErrorCategory"`
	// Retryable / Terminal classify a surfaced failing source against the current
	// per-source budget: Retryable = FailingAttempts < MaxRetries (a later cycle, or an
	// owner retry, will try it again), Terminal = FailingAttempts >= MaxRetries (this
	// source has given up on this chapter until an owner retry resets it). Both are false
	// when the row has no failing source.
	Retryable bool `json:"retryable"`
	Terminal  bool `json:"terminal"`

	WaitingReason string     `json:"waitingReason"`
	DeferredUntil *time.Time `json:"deferredUntil"`
	DeferReason   string     `json:"deferReason"`
	Retries       int        `json:"retries"`
	NextAttemptAt *time.Time `json:"nextAttemptAt"`
	LastError     string     `json:"lastError"`
	ErrorCategory string     `json:"errorCategory"`
	Filename      string     `json:"filename"`
	PageCount     *int       `json:"pageCount"`
	DownloadDate  *time.Time `json:"downloadDate"`
}

// DownloadSummaryDTO is the GET /api/downloads/summary response: the three global
// nav-badge counts, computed from ONE grouped aggregate over Chapter.state (see
// Service.Summary). Downloading = chapters fetching now; Queued = wanted chapters
// waiting to download; Failed = failed + permanently_failed (both retryable, both
// "needs attention"). Cheap enough to poll for a persistent nav badge.
type DownloadSummaryDTO struct {
	Downloading int `json:"downloading"`
	Queued      int `json:"queued"`
	Failed      int `json:"failed"`
}

// rowContext holds the per-chapter values resolveRow derives once (from data already
// in memory — the batch-loaded feeds + the single breaker snapshot) and the DTO
// mapper projects: the resolved source (id + label + its per-source attempt count),
// the current retry budget, the fresh-vs-upgrade marker + target, and the waiting
// status (reason category + retry ETA + detail message). Passing one struct keeps the
// mapper's signature small as the enrichment grows.
type rowContext struct {
	provider      string
	providerName  string
	attempts      int
	maxRetries    int
	isUpgrade     bool
	upgradeTarget string

	failingProvider      string
	failingProviderName  string
	failingAttempts      int
	failingLastError     string
	failingErrorCategory string
	retryable            bool
	terminal             bool

	waitingReason string
	retryAt       *time.Time
	deferReason   string
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
// (chapterSource). Both answers are "the highest-importance source whose feed
// carries this key", which is exactly the scheduler's primary-source rule, so they
// share one definition (§2 DRY) and cost no extra query. A chapter no feed carries
// resolves to no source at all ("").
type seriesResolution struct {
	names          map[string]string
	displayName    string
	coverURL       string
	upgradeTargets upgradeTargetIndex
}

// newDownloadChapterDTO maps one Chapter row to its enriched DTO. The series + source
// context (display name, category, cover, chapter name, provider id + label, attempt
// count, retry budget, upgrade marker + target, waiting status) is resolved once per
// chapter by the caller (resolveRow) and passed in as rc, so this mapper does no
// lookups — it only projects fields, ensuring every contract field is populated (§16).
func newDownloadChapterDTO(ch *ent.Chapter, category string, res seriesResolution, rc rowContext) DownloadChapterDTO {
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
		Provider:       rc.provider,
		ProviderName:   rc.providerName,
		Attempts:       rc.attempts,
		MaxRetries:     rc.maxRetries,
		IsUpgrade:      rc.isUpgrade,
		UpgradeTarget:  rc.upgradeTarget,

		FailingProvider:      rc.failingProvider,
		FailingProviderName:  rc.failingProviderName,
		FailingAttempts:      rc.failingAttempts,
		FailingLastError:     rc.failingLastError,
		FailingErrorCategory: rc.failingErrorCategory,
		Retryable:            rc.retryable,
		Terminal:             rc.terminal,

		WaitingReason: rc.waitingReason,
		DeferredUntil: rc.retryAt,
		DeferReason:   rc.deferReason,
		Retries:       ch.Retries,
		NextAttemptAt: ch.NextAttemptAt,
		LastError:     ch.LastError,
		ErrorCategory: ch.ErrorCategory,
		Filename:      ch.Filename,
		PageCount:     ch.PageCount,
		DownloadDate:  ch.DownloadDate,
	}
}
