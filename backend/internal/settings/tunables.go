// Package settings is the runtime-tunable configuration overlay. It sits on top
// of the env-sourced config defaults (internal/config remains the SOLE env
// boundary) and exposes a CLOSED allowlist of keys whose values an owner may
// change at runtime — stored in the Settings DB table — without a restart.
//
// Resolution for every tunable is: the DB override if a (valid) row exists, else
// the config-resolved default injected at construction. Consumers (the download
// dispatcher, the job tickers, the refresh sweep, the health computation) read
// the typed accessors at the point of use, so a change takes effect on the next
// cycle (hot reload). The service NEVER reads the environment — main injects the
// Defaults built from *config.Config, preserving the single env boundary.
package settings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrUnknownSetting is returned by Set when a key is not in the closed allowlist.
// The HTTP handler maps it to a 400 — the API never writes arbitrary keys.
var ErrUnknownSetting = errors.New("unknown setting key")

// ErrInvalidSetting is returned by Set when a value fails its per-key validation
// (unparseable or out of bounds). The HTTP handler maps it to a 400. Set is
// fail-closed: it rejects invalid values so the store never holds one.
var ErrInvalidSetting = errors.New("invalid setting value")

// Type classifies a tunable's value representation for the API client (the FE
// renders a duration picker vs a number input accordingly).
type Type string

const (
	// TypeDuration is a Go duration string (e.g. "15m0s", "2h0m0s").
	TypeDuration Type = "duration"
	// TypeInt is a base-10 integer.
	TypeInt Type = "int"
	// TypeBool is a boolean ("true"/"false").
	TypeBool Type = "bool"
)

// The closed allowlist of runtime-tunable keys. These are the ONLY keys the
// settings API will read or write; anything else is ErrUnknownSetting.
const (
	// KeyDownloadInterval is the download ticker period (duration, >= 1m).
	KeyDownloadInterval = "jobs.download_interval"
	// KeyDownloadConcurrency is the PER-SOURCE download concurrency cap (int,
	// 1..32): how many chapters from the same source the dispatcher fetches in
	// parallel, and how many of a source's queued chapters may be "downloading" at
	// once. Default 5 (Kaizoku parity). Read at use-time so a change applies on the
	// next download cycle without a restart.
	KeyDownloadConcurrency = "jobs.download_concurrency"
	// KeyRefreshInterval is the discovery-sweep ticker period (duration, >= 10m).
	KeyRefreshInterval = "jobs.refresh_interval"
	// KeyRefreshConcurrency bounds parallel provider re-fetches (int, 1..32).
	KeyRefreshConcurrency = "jobs.refresh_concurrency"
	// KeyMaxRetries is the PER-SOURCE download retry budget (int, 1..20): how many
	// times a chapter is retried from ONE source before that source is abandoned
	// for it. A chapter is only permanently_failed once EVERY source that offers it
	// has been abandoned — this is not a global per-chapter counter. The lower bound
	// is 1 (NOT 0): with the attempts>=maxRetries exhaustion rule, a max of 0 would
	// make every fresh source exhausted before any fetch, driving the whole library
	// straight to permanently_failed — so a source must always get at least one try.
	KeyMaxRetries = "jobs.max_retries"
	// KeyRetryBackoff is the base retry backoff delay (duration, >= 1s).
	KeyRetryBackoff = "jobs.retry_backoff"
	// KeyStaleGraceDays tunes the M7 source-health stale threshold (int, 0..365).
	KeyStaleGraceDays = "health.stale_grace_days"
	// KeyExtensionCheckInterval is the extension auto-check ticker period
	// (duration, 0 = disabled or >= 1h).
	KeyExtensionCheckInterval = "jobs.extension_check_interval"
	// KeyWarmupInterval is the anti-bot session warm-up ticker period
	// (duration, 0 = disabled or >= 1m).
	KeyWarmupInterval = "jobs.warmup_interval"
	// KeyWarmupSlowThresholdMs is the EWMA-latency threshold in milliseconds above
	// which a source is warmed by the WarmSlow pass (int, 100..600000).
	KeyWarmupSlowThresholdMs = "jobs.warmup_slow_threshold_ms"
	// KeySearchCacheTTL is the lifetime of a cached interactive Search fan-out
	// result (duration, 0 = caching disabled or >= 1s). Read PER-Get by the search
	// cache, so a change hot-reloads and applies to entries already stored.
	KeySearchCacheTTL = "jobs.search_cache_ttl"
	// KeyChapterCacheTTL is the lifetime of a cached FetchChapters result for the
	// interactive coverage→configure→adopt discovery flow (duration, 0 = caching
	// disabled or >= 1s). Read PER-Get (hot reload). The refresh sweep bypasses this
	// cache (it fetches fresh), so a long TTL here never stales-out discovery.
	KeyChapterCacheTTL = "jobs.chapter_cache_ttl"
	// KeySourcesFailureThreshold is the consecutive-failure count (int, >= 1)
	// after which the source-politeness circuit-breaker (internal/sourcegate)
	// trips a physical source into cooldown. Default 5.
	//
	// RELATIONSHIP TO jobs.max_retries: the breaker counts CONSECUTIVE failures
	// ACROSS chapters for one physical source, whereas max_retries is the
	// PER-(source,chapter) budget. So a genuinely-down source with many failing
	// chapters trips the breaker quickly (protecting the IP), but a very small
	// backlog — fewer wanted chapters than failure_threshold — may per-chapter
	// exhaust (each chapter hits max_retries) before the breaker ever trips.
	// That is acceptable (a tiny backlog is not the IP-block scenario the breaker
	// guards); tune failure_threshold DOWN if a deployment wants the breaker to
	// engage on a smaller run of failures.
	KeySourcesFailureThreshold = "sources.failure_threshold"
	// KeySourcesCooldown is how long a tripped source's circuit-breaker stays
	// open (duration, >= 1m) before it is available again. Default 30m.
	KeySourcesCooldown = "sources.cooldown"
	// KeySourcesMinRequestDelay is the minimum politeness delay (duration, 0 =
	// disabled) enforced between successive requests to the SAME physical
	// source, independent of the per-source concurrency cap. Default 500ms
	// (Kaizoku.GO parity).
	KeySourcesMinRequestDelay = "sources.min_request_delay"
	// KeySuppressSplitParts toggles fractional-part suppression.
	KeySuppressSplitParts = "jobs.suppress_split_parts"
)

// Defaults carries the config-resolved default for every tunable key. main
// builds it from *config.Config (see settings wiring in cmd/tsundoku) and injects
// it into NewService, so the settings layer never touches os.Getenv — the single
// env boundary stays in internal/config.
type Defaults struct {
	DownloadInterval        time.Duration
	DownloadConcurrency     int
	RefreshInterval         time.Duration
	RefreshConcurrency      int
	MaxRetries              int
	RetryBackoff            time.Duration
	StaleGraceDays          int
	ExtensionCheckInterval  time.Duration
	WarmupInterval          time.Duration
	WarmupSlowThresholdMs   int
	SearchCacheTTL          time.Duration
	ChapterCacheTTL         time.Duration
	SourcesFailureThreshold int
	SourcesCooldown         time.Duration
	SourcesMinRequestDelay  time.Duration
	SuppressSplitParts      bool
}

// tunable is one allowlisted key's metadata + validation. validate parses a raw
// value and checks its bounds, returning the canonical string to store (so the
// DB always holds a normalized, re-parseable value) or an ErrInvalidSetting. def
// returns the key's default as that same canonical string, sourced from the
// injected Defaults. One tunable is the SINGLE source of truth for a key, shared
// by the typed accessors, List, and Set (DRY — no validation logic is duplicated).
type tunable struct {
	key      string
	typ      Type
	unit     string
	validate func(raw string) (canonical string, err error)
	def      func(d Defaults) string
}

// tunableOrder fixes the stable order List returns the allowlist in (so the FE
// Settings screen renders deterministically and the drift gate stays stable).
var tunableOrder = []string{
	KeyDownloadInterval,
	KeyDownloadConcurrency,
	KeyRefreshInterval,
	KeyRefreshConcurrency,
	KeyMaxRetries,
	KeyRetryBackoff,
	KeyStaleGraceDays,
	KeyExtensionCheckInterval,
	KeyWarmupInterval,
	KeyWarmupSlowThresholdMs,
	KeySearchCacheTTL,
	KeyChapterCacheTTL,
	KeySourcesFailureThreshold,
	KeySourcesCooldown,
	KeySourcesMinRequestDelay,
	KeySuppressSplitParts,
}

// tunables is the key→tunable registry, built once from the bounds in the design
// spec's allowlist table. It is the closed set the API may touch.
var tunables = map[string]tunable{
	KeyDownloadInterval: durationTunable(
		KeyDownloadInterval, "duration", time.Minute,
		func(d Defaults) time.Duration { return d.DownloadInterval },
	),
	KeyDownloadConcurrency: intTunable(
		KeyDownloadConcurrency, "count", 1, 32,
		func(d Defaults) int { return d.DownloadConcurrency },
	),
	KeyRefreshInterval: durationTunable(
		KeyRefreshInterval, "duration", 10*time.Minute,
		func(d Defaults) time.Duration { return d.RefreshInterval },
	),
	KeyRefreshConcurrency: intTunable(
		KeyRefreshConcurrency, "count", 1, 32,
		func(d Defaults) int { return d.RefreshConcurrency },
	),
	KeyMaxRetries: intTunable(
		KeyMaxRetries, "count", 1, 20,
		func(d Defaults) int { return d.MaxRetries },
	),
	KeyRetryBackoff: durationTunable(
		KeyRetryBackoff, "duration", time.Second,
		func(d Defaults) time.Duration { return d.RetryBackoff },
	),
	KeyStaleGraceDays: intTunable(
		KeyStaleGraceDays, "days", 0, 365,
		func(d Defaults) int { return d.StaleGraceDays },
	),
	KeyExtensionCheckInterval: durationTunableMinOrZero(
		KeyExtensionCheckInterval, "duration", time.Hour,
		func(d Defaults) time.Duration { return d.ExtensionCheckInterval },
	),
	KeyWarmupInterval: durationTunableMinOrZero(
		KeyWarmupInterval, "duration", time.Minute,
		func(d Defaults) time.Duration { return d.WarmupInterval },
	),
	KeyWarmupSlowThresholdMs: intTunable(
		KeyWarmupSlowThresholdMs, "milliseconds", 100, 600000,
		func(d Defaults) int { return d.WarmupSlowThresholdMs },
	),
	// KeySearchCacheTTL / KeyChapterCacheTTL: 0 = caching disabled, else >= 1s.
	// durationTunableMinOrZero gives them the same "off or floor" shape as the
	// warm-up interval so an owner can turn a cache off live.
	KeySearchCacheTTL: durationTunableMinOrZero(
		KeySearchCacheTTL, "duration", time.Second,
		func(d Defaults) time.Duration { return d.SearchCacheTTL },
	),
	KeyChapterCacheTTL: durationTunableMinOrZero(
		KeyChapterCacheTTL, "duration", time.Second,
		func(d Defaults) time.Duration { return d.ChapterCacheTTL },
	),
	// KeySourcesFailureThreshold's upper bound (100) is not spec-mandated — it is
	// a sanity ceiling consistent with the other bounded int tunables, since a
	// threshold that high would make the breaker effectively never trip.
	KeySourcesFailureThreshold: intTunable(
		KeySourcesFailureThreshold, "count", 1, 100,
		func(d Defaults) int { return d.SourcesFailureThreshold },
	),
	KeySourcesCooldown: durationTunable(
		KeySourcesCooldown, "duration", time.Minute,
		func(d Defaults) time.Duration { return d.SourcesCooldown },
	),
	// KeySourcesMinRequestDelay allows 0 (politeness disabled, pure concurrency
	// behaviour) or any positive duration — there is no floor above 0, unlike
	// the other "0 or >= min" tunables, so durationTunableMinOrZero's minVal is 0.
	KeySourcesMinRequestDelay: durationTunableMinOrZero(
		KeySourcesMinRequestDelay, "duration", 0,
		func(d Defaults) time.Duration { return d.SourcesMinRequestDelay },
	),
	KeySuppressSplitParts: boolTunable(
		KeySuppressSplitParts,
		func(d Defaults) bool { return d.SuppressSplitParts },
	),
}

// durationTunable builds a duration-typed tunable that rejects values below min
// and stores the canonical time.Duration string form.
func durationTunable(key, unit string, minVal time.Duration, def func(Defaults) time.Duration) tunable {
	return tunable{
		key:  key,
		typ:  TypeDuration,
		unit: unit,
		validate: func(raw string) (string, error) {
			d, err := time.ParseDuration(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid duration", ErrInvalidSetting, key, raw)
			}
			if d < minVal {
				return "", fmt.Errorf("%w: %s must be at least %s (got %s)", ErrInvalidSetting, key, minVal, d)
			}
			return d.String(), nil
		},
		def: func(d Defaults) string { return def(d).String() },
	}
}

// durationTunableMinOrZero is like durationTunable but additionally accepts 0 as
// a special "disabled" sentinel stored canonically as "0s". Any positive value
// must still satisfy >= minVal. Used for jobs whose interval can be toggled off
// at runtime without redeploying the binary.
func durationTunableMinOrZero(key, unit string, minVal time.Duration, def func(Defaults) time.Duration) tunable {
	return tunable{
		key:  key,
		typ:  TypeDuration,
		unit: unit,
		validate: func(raw string) (string, error) {
			d, err := time.ParseDuration(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid duration", ErrInvalidSetting, key, raw)
			}
			if d == 0 {
				return "0s", nil // 0 = disabled; canonical form
			}
			if d < minVal {
				return "", fmt.Errorf("%w: %s must be 0 (disabled) or at least %s (got %s)", ErrInvalidSetting, key, minVal, d)
			}
			return d.String(), nil
		},
		def: func(d Defaults) string { return def(d).String() },
	}
}

// intTunable builds an int-typed tunable that rejects values outside [lo,hi] and
// stores the canonical base-10 string form.
func intTunable(key, unit string, lo, hi int, def func(Defaults) int) tunable {
	return tunable{
		key:  key,
		typ:  TypeInt,
		unit: unit,
		validate: func(raw string) (string, error) {
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid integer", ErrInvalidSetting, key, raw)
			}
			if n < lo || n > hi {
				return "", fmt.Errorf("%w: %s must be in [%d, %d] (got %d)", ErrInvalidSetting, key, lo, hi, n)
			}
			return strconv.Itoa(n), nil
		},
		def: func(d Defaults) string { return strconv.Itoa(def(d)) },
	}
}

// boolTunable builds a bool-typed tunable, storing the canonical
// "true"/"false" string form.
func boolTunable(key string, def func(Defaults) bool) tunable {
	return tunable{
		key:  key,
		typ:  TypeBool,
		unit: "",
		validate: func(raw string) (string, error) {
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid boolean", ErrInvalidSetting, key, raw)
			}
			return strconv.FormatBool(b), nil
		},
		def: func(d Defaults) string { return strconv.FormatBool(def(d)) },
	}
}
