package series

import (
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
)

// Source-health status values for a single provider within a series.
const (
	// HealthOK means the source is reachable and not stale.
	HealthOK = "ok"
	// HealthStale means the source has fallen behind the series' leading edge
	// AND its newest chapter is older than the configured grace period.
	HealthStale = "stale"
	// HealthErroring means the last refresh of this source failed.
	HealthErroring = "erroring"
)

// ProviderHealth is the computed, read-only health of one source within a series.
type ProviderHealth struct {
	// Status is one of HealthOK / HealthStale / HealthErroring.
	Status string
	// ChaptersBehind is how many of the series' distinct chapter keys this
	// source lacks (informational; never gates Status).
	ChaptersBehind int
	// NewestChapterAt is the max provider upload date this source carries (nil
	// if the source supplied no dates / has no chapters).
	NewestChapterAt *time.Time
	// LastSyncedAt is when a refresh last successfully fetched this source (nil
	// if never refreshed).
	LastSyncedAt *time.Time
	// LastError is the last refresh error for this source ("" if none).
	LastError string
}

// ProviderHealthInput is the already-loaded data ComputeProviderHealth needs.
type ProviderHealthInput struct {
	// SyncState is this source's persisted polling state (nil if no row yet).
	SyncState *ent.SuwayomiSyncState
	// ProviderChapters is this source's availability feed.
	ProviderChapters []*ent.ProviderChapter
	// SeriesChapterKeys is the set of all distinct chapter keys in the series.
	SeriesChapterKeys map[string]struct{}
	// SeriesMaxNumber is the series' leading-edge chapter number (nil if none
	// numbered).
	SeriesMaxNumber *float64
	// MultiSource is true when the series has more than one provider.
	MultiSource bool
	// Completed is true when the owner has marked the series finished. A
	// completed series is excluded from health: its status is forced to
	// HealthOK (it is done, not broken), never stale or erroring.
	Completed bool
}

// providerMaxNumber returns the maximum non-nil Number across chs, or nil if
// none of the entries carry a number.
func providerMaxNumber(chs []*ent.ProviderChapter) *float64 {
	var max *float64
	for _, pc := range chs {
		if pc.Number != nil && (max == nil || *pc.Number > *max) {
			n := *pc.Number
			max = &n
		}
	}
	return max
}

// newestUpload returns the maximum non-nil ProviderUploadDate across chs, or
// nil if none of the entries carry a date.
func newestUpload(chs []*ent.ProviderChapter) *time.Time {
	var newest *time.Time
	for _, pc := range chs {
		if pc.ProviderUploadDate != nil && (newest == nil || pc.ProviderUploadDate.After(*newest)) {
			newest = pc.ProviderUploadDate
		}
	}
	return newest
}

// countBehind returns how many keys in seriesKeys are absent from the have set.
func countBehind(have map[string]struct{}, seriesKeys map[string]struct{}) int {
	n := 0
	for k := range seriesKeys {
		if _, ok := have[k]; !ok {
			n++
		}
	}
	return n
}

// isStale reports whether a provider has genuinely fallen behind the series'
// leading edge and remained there long enough to warrant a stale signal.
// The multi-source guard is the caller's responsibility (single-source carve-out
// belongs to ComputeProviderHealth, not here).
func isStale(seriesMax *float64, providerMax *float64, newestChapterAt *time.Time, now time.Time, graceDays int) bool {
	behindLeadingEdge := seriesMax != nil && providerMax != nil && *providerMax < *seriesMax
	pastGrace := newestChapterAt != nil && newestChapterAt.Before(now.AddDate(0, 0, -graceDays))
	return behindLeadingEdge && pastGrace
}

// ComputeProviderHealth derives one source's health from already-loaded data.
// now and graceDays are passed in so the result is deterministic and testable.
// Status precedence: erroring > stale > ok.
func ComputeProviderHealth(in ProviderHealthInput, now time.Time, graceDays int) ProviderHealth {
	h := ProviderHealth{Status: HealthOK}

	if in.SyncState != nil {
		h.LastSyncedAt = in.SyncState.LastSyncedAt
		h.LastError = in.SyncState.LastError
	}

	have := make(map[string]struct{}, len(in.ProviderChapters))
	for _, pc := range in.ProviderChapters {
		have[pc.ChapterKey] = struct{}{}
	}

	h.ChaptersBehind = countBehind(have, in.SeriesChapterKeys)
	h.NewestChapterAt = newestUpload(in.ProviderChapters)
	providerMax := providerMaxNumber(in.ProviderChapters)

	// A completed series is done, not broken: surface the informational fields
	// but never escalate to stale/erroring. One rule, reused by every caller.
	if in.Completed {
		return h
	}

	if h.LastError != "" {
		h.Status = HealthErroring
		return h
	}

	if in.MultiSource && isStale(in.SeriesMaxNumber, providerMax, h.NewestChapterAt, now, graceDays) {
		h.Status = HealthStale
	}
	return h
}
