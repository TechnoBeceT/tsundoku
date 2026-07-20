package engine

import (
	"sort"
	"time"

	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// The two states a source-status row can be in. A source appears on the strip only
// when it is DOING something — downloading (≥1 chapter in flight) or cooling (its
// anti-ban circuit-breaker is tripped) — so the fully-idle majority is omitted.
const (
	sourceStateDownloading = "downloading"
	sourceStateCooling     = "cooling"
)

// SourceStatusDTO is one row of GET /api/engine/sources: a source that is actively
// downloading OR in an anti-ban cooldown right now. Every field is a pure DB /
// in-memory read (active counts from the download read-model, cooldown/failure
// signals from the persisted circuit-breaker) — the endpoint makes NO engine call.
type SourceStatusDTO struct {
	// SourceKey is the canonical physical-source name (the breaker/metrics key).
	SourceKey string `json:"sourceKey"`
	// State is "downloading" or "cooling".
	State string `json:"state"`
	// ActiveCount is how many chapters are being fetched from this source now
	// (0 for a cooling source with nothing in flight).
	ActiveCount int `json:"activeCount"`
	// Cap is the current per-source download-concurrency cap (the "N/cap"
	// denominator), read at use-time so an owner's settings change is reflected.
	Cap int `json:"cap"`
	// CooldownRemainingSeconds is how long a cooling source's breaker stays tripped
	// (0 for a downloading source, or once the cooldown has elapsed).
	CooldownRemainingSeconds int `json:"cooldownRemainingSeconds"`
	// Reason is the classified failure category of a cooling source's last error
	// (errorclass, e.g. "rate_limit" / "server_error"); "" for a downloading source.
	Reason string `json:"reason"`
	// ConsecutiveFailures is the source's current failure streak (0 when no breaker
	// row exists).
	ConsecutiveFailures int `json:"consecutiveFailures"`
	// LastError is the source's most recent recorded failure message ("" when none).
	LastError string `json:"lastError"`
}

// buildSourceStatuses merges the per-source ACTIVE counts (from the download
// read-model) with the circuit-breaker snapshot into the strip's rows, keeping ONLY
// the sources that are downloading (activeCount>0) or cooling (breaker tripped at
// now), and sorting downloading rows first, then by name. A source that is both
// downloading and cooling is reported as downloading (its in-flight chapters are
// meaningfully in progress) while still carrying its breaker's failure counters.
func buildSourceStatuses(active map[string]int, breakers map[string]sourcegate.BreakerState, cap int, now time.Time) []SourceStatusDTO {
	out := []SourceStatusDTO{}
	seen := map[string]bool{}

	for key, count := range active {
		if count <= 0 {
			continue
		}
		out = append(out, downloadingStatus(key, count, cap, breakers[key]))
		seen[key] = true
	}
	for key, b := range breakers {
		if seen[key] || !b.IsCoolingDown(now) {
			continue
		}
		out = append(out, coolingStatus(key, cap, b, now))
	}

	sortSourceStatuses(out)
	return out
}

// downloadingStatus builds a "downloading" row: N/cap chapters in flight, carrying
// any breaker failure counters (breakers[key] is the zero BreakerState when the
// source has no breaker row, so failures/lastError read 0/"").
func downloadingStatus(key string, count, cap int, b sourcegate.BreakerState) SourceStatusDTO {
	return SourceStatusDTO{
		SourceKey:           key,
		State:               sourceStateDownloading,
		ActiveCount:         count,
		Cap:                 cap,
		ConsecutiveFailures: b.ConsecutiveFailures,
		LastError:           b.LastError,
	}
}

// coolingStatus builds a "cooling" row: the remaining cooldown (clamped to ≥0), the
// classified failure reason, and the breaker's failure counters. ActiveCount is 0 —
// a tripped source is excluded from candidacy, so nothing new is fetching from it.
func coolingStatus(key string, cap int, b sourcegate.BreakerState, now time.Time) SourceStatusDTO {
	remaining := 0
	if b.CooldownUntil != nil {
		if secs := int(b.CooldownUntil.Sub(now).Seconds()); secs > 0 {
			remaining = secs
		}
	}
	return SourceStatusDTO{
		SourceKey:                key,
		State:                    sourceStateCooling,
		Cap:                      cap,
		CooldownRemainingSeconds: remaining,
		Reason:                   errorclass.ClassifyMessage(b.LastError),
		ConsecutiveFailures:      b.ConsecutiveFailures,
		LastError:                b.LastError,
	}
}

// sortSourceStatuses orders the strip deterministically: downloading rows first
// (they are the "happening now" signal), then cooling rows, each group by source
// name. Deterministic order keeps the endpoint's output stable across polls.
func sortSourceStatuses(list []SourceStatusDTO) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].State != list[j].State {
			return list[i].State == sourceStateDownloading
		}
		return list[i].SourceKey < list[j].SourceKey
	})
}
