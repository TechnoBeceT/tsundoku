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

	"github.com/technobecet/tsundoku/internal/pkg/urlx"
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
	// TypeString is a free-form or constrained string value (e.g. a URL or a
	// session name) — added for the FlareSolverr tunables, the first
	// string-shaped keys in the allowlist.
	TypeString Type = "string"
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
	// KeyRetryBackoff is the FLAT retry interval (duration, >= 1s): the constant
	// gap before every retry of a chapter from a source (no per-attempt growth).
	// Default 30m.
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
	// KeyTrackRetryInterval is the tracker-push retry-queue drain period
	// (duration, >= 30s — always-on, no disabled sentinel: see
	// job.Intervals.TrackRetryInterval's doc comment). Read at use-time by
	// job.Runner.StartTrackerRetry, so a change hot-reloads on the next pass.
	KeyTrackRetryInterval = "jobs.track_retry_interval"
	// KeyAutoUpdateTrack gates the reading-triggered tracker-sync push (bool,
	// default true — spec/trackers-sync-phase4 §2 trigger (a)): when false,
	// SetProgress marking a chapter read never fires a PushProgress call.
	// Read at use-time by syncsvc.Service.PushProgress (via the
	// AutoUpdateTracker port), so a change hot-reloads on the very next
	// reader progress write — it does NOT gate manual "sync now"/tracking-
	// sheet edits (those are explicit owner actions, never best-effort).
	KeyAutoUpdateTrack = "trackers.auto_update_track"
	// KeyMetadataAutoIdentify gates the Phase-1 native metadata engine's
	// background auto-identify pass (bool, default true): when false, a
	// freshly adopted/imported series' metadata is left untouched until the
	// owner manually identifies it. Read at use-time by
	// metadatasvc.Service.AutoIdentify (via the WithAutoIdentifyGate port),
	// so a change hot-reloads on the very next adopt/import. This is a
	// SEPARATE gate from Series.metadata_locked (the per-series "owner
	// hand-curated" flag, set by IdentifyMerge) — either one suppresses
	// AutoIdentify independently.
	KeyMetadataAutoIdentify = "metadata.auto_identify"
	// KeyFlareSolverrEnabled toggles Tsundoku's own use of FlareSolverr (bool,
	// default false). See the doc comment block below (QCAT-238): this whole
	// flaresolverr.* group is TSUNDOKU-OWNED — it is NOT read from Suwayomi and
	// NOT an env var. Kitsu's Cloudflare-clearing transport (internal/tracker/
	// kitsu) resolves it at request-time; on save the FlareSolverr handler
	// best-effort MIRRORS the same values down to Suwayomi's own settings via
	// suwayomi.Client.SetServerSettings, so Suwayomi's source-scraping stays in
	// sync while Suwayomi still exists.
	KeyFlareSolverrEnabled = "flaresolverr.enabled"
	// KeyFlareSolverrURL is the FlareSolverr endpoint (e.g. http://host:8191).
	// Blank (default) disables it regardless of KeyFlareSolverrEnabled — the
	// Kitsu transport passes through untouched when the URL is empty.
	KeyFlareSolverrURL = "flaresolverr.url"
	// KeyFlareSolverrTimeout is the per-request solve timeout in seconds
	// (int, 5..600, default 60).
	KeyFlareSolverrTimeout = "flaresolverr.timeout"
	// KeyFlareSolverrSessionName is the FlareSolverr session identifier
	// (free-form string, default "" — FlareSolverr treats a blank session as
	// "no session", solving fresh every time).
	KeyFlareSolverrSessionName = "flaresolverr.session_name"
	// KeyFlareSolverrSessionTTL is the session time-to-live in minutes
	// (int, 0..1440, default 15) — also the local cf_clearance cache TTL the
	// Kitsu transport uses before it re-solves.
	KeyFlareSolverrSessionTTL = "flaresolverr.session_ttl"
	// KeyFlareSolverrResponseFallback mirrors Suwayomi's own
	// asResponseFallback flag (bool, default false): whether FlareSolverr is
	// used only as a fallback for a blocked request rather than proactively.
	// Tsundoku stores + mirrors this value but its own Kitsu transport always
	// behaves in the "fallback" shape (direct request first, solve only on a
	// detected Cloudflare challenge) regardless of this flag's value — see
	// internal/tracker/kitsu's Cloudflare-clearing transport doc comment.
	KeyFlareSolverrResponseFallback = "flaresolverr.response_fallback"
	// KeyNotificationsEnabled is the global new-chapter notifications toggle
	// (bool, default true): when false the internal/notify pass skips entirely
	// (no SSE chapter.new, no Web Push) without advancing its watermark, so
	// re-enabling later never storms the owner. Read at use-time by the notifier
	// (via the Toggle port), so a change hot-reloads on the next download cycle.
	KeyNotificationsEnabled = "notifications.enabled"
	// KeyEngineSocksEnabled toggles routing the engine's source traffic through
	// a SOCKS proxy (bool, default false). Like the flaresolverr.* group this is
	// TSUNDOKU-OWNED config seeded FROM the engine's own SOCKS settings
	// (enginetopo.SeedEngineConfig) and mirrored back the same way the
	// FlareSolverr group is (QCAT-238's pattern, applied to SOCKS) — NOT read
	// from an env var.
	KeyEngineSocksEnabled = "engine.socks_enabled"
	// KeyEngineSocksHost is the SOCKS proxy hostname or IP (free-form string,
	// default "").
	KeyEngineSocksHost = "engine.socks_host"
	// KeyEngineSocksPort is the SOCKS proxy port (int, 1..65535, default 1080 —
	// the IANA-registered SOCKS port). Suwayomi types this as a String on its
	// own wire (see suwayomi.SuwayomiSettings.SocksProxyPort's doc comment);
	// Tsundoku's own copy is validated as a numeric int like every other
	// bounded int tunable.
	KeyEngineSocksPort = "engine.socks_port"
	// KeyEngineSocksVersion is the SOCKS protocol version (int, MUST be 4 or 5
	// — not a contiguous range, unlike every other int tunable — default 5).
	KeyEngineSocksVersion = "engine.socks_version"
	// KeyReportingRetentionDays is how many days of source-operation audit-log
	// rows (SourceEvent) the daily retention purge keeps (int, 1..3650, default
	// 30). Read at USE-TIME by the retention-purge ticker, so a change hot-reloads
	// on the next daily sweep without a restart.
	KeyReportingRetentionDays = "reporting.retention_days"
	// KeyRetainedVersions is how many .apk versions per extension the apk cache
	// keeps (int, 1..20, default 3) — the depth of the reversible-update history.
	// 1 means "keep only the current version" (no rollback history). A harvest or
	// update prunes each extension's cached .apks to the newest N ∪ the installed
	// version; the retained set is surfaced in the Extensions UI for reinstall.
	// Read at USE-TIME (harvest/update write-through), so a change hot-reloads on
	// the next prune without a restart.
	KeyRetainedVersions = "extensions.retained_versions"
)

// retainedVersionsMin/Max bound the extensions.retained_versions tunable.
const (
	retainedVersionsMin = 1
	retainedVersionsMax = 20
)

// reportingRetentionMin/Max bound the reporting.retention_days tunable (1 day
// up to ~10 years — an upper sanity ceiling consistent with the other bounded
// int tunables).
const (
	reportingRetentionMin = 1
	reportingRetentionMax = 3650
)

// flareSolverrTimeoutMin/Max and flareSolverrSessionTTLMin/Max bound the two
// FlareSolverr int tunables (seconds / minutes respectively).
const (
	flareSolverrTimeoutMin    = 5
	flareSolverrTimeoutMax    = 600
	flareSolverrSessionTTLMin = 0
	flareSolverrSessionTTLMax = 1440
)

// engineSocksPortMin/Max bound the SOCKS proxy port tunable; engineSocksVersions
// is the closed set of valid SOCKS protocol versions.
const (
	engineSocksPortMin = 1
	engineSocksPortMax = 65535
)

var engineSocksVersions = []int{4, 5}

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
	TrackRetryInterval      time.Duration
	AutoUpdateTrack         bool
	MetadataAutoIdentify    bool
	// FlareSolverrEnabled..FlareSolverrResponseFallback back the Tsundoku-owned
	// FlareSolverr tunables (QCAT-238). Unlike every other Defaults field these
	// are NOT sourced from cfg.* in defaultsFromConfig — there is deliberately
	// no env var for them — but they still flow through this single bridge
	// struct so the settings layer never special-cases where a default comes
	// from.
	FlareSolverrEnabled          bool
	FlareSolverrURL              string
	FlareSolverrTimeout          int
	FlareSolverrSessionName      string
	FlareSolverrSessionTTL       int
	FlareSolverrResponseFallback bool
	// NotificationsEnabled backs the global new-chapter notifications toggle
	// (default true). Like the FlareSolverr defaults it has no env var — main
	// injects a constant true via defaultsFromConfig — but it still flows through
	// this single bridge so the settings layer never special-cases a default's
	// origin.
	NotificationsEnabled bool
	// EngineSocksEnabled..EngineSocksVersion back the engine.socks_* tunables
	// (SOCKS-proxy subset of the engine's server settings). Like the
	// FlareSolverr defaults these have no env var — main injects fixed factory
	// defaults via defaultsFromConfig, overridden in practice by
	// enginetopo.SeedEngineConfig's one-shot seed from the live engine.
	EngineSocksEnabled bool
	EngineSocksHost    string
	EngineSocksPort    int
	EngineSocksVersion int
	// RetainedVersions backs the extensions.retained_versions tunable — the depth
	// of the apk-cache rollback history (env-sourced default, unlike the
	// FlareSolverr/SOCKS groups which have no env var).
	RetainedVersions int
	// ReportingRetentionDays backs the reporting.retention_days tunable — how many
	// days of source-operation audit-log rows the daily purge keeps.
	ReportingRetentionDays int
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
	KeyTrackRetryInterval,
	KeyAutoUpdateTrack,
	KeyMetadataAutoIdentify,
	KeyFlareSolverrEnabled,
	KeyFlareSolverrURL,
	KeyFlareSolverrTimeout,
	KeyFlareSolverrSessionName,
	KeyFlareSolverrSessionTTL,
	KeyFlareSolverrResponseFallback,
	KeyNotificationsEnabled,
	KeyEngineSocksEnabled,
	KeyEngineSocksHost,
	KeyEngineSocksPort,
	KeyEngineSocksVersion,
	KeyReportingRetentionDays,
	KeyRetainedVersions,
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
	KeyTrackRetryInterval: durationTunable(
		KeyTrackRetryInterval, "duration", 30*time.Second,
		func(d Defaults) time.Duration { return d.TrackRetryInterval },
	),
	KeyAutoUpdateTrack: boolTunable(
		KeyAutoUpdateTrack,
		func(d Defaults) bool { return d.AutoUpdateTrack },
	),
	KeyMetadataAutoIdentify: boolTunable(
		KeyMetadataAutoIdentify,
		func(d Defaults) bool { return d.MetadataAutoIdentify },
	),
	// The FlareSolverr group (QCAT-238, Tsundoku-owned Cloudflare-bypass
	// config — see the KeyFlareSolverr* doc comments above).
	KeyFlareSolverrEnabled: boolTunable(
		KeyFlareSolverrEnabled,
		func(d Defaults) bool { return d.FlareSolverrEnabled },
	),
	KeyFlareSolverrURL: urlOrBlankTunable(
		KeyFlareSolverrURL, "url",
		func(d Defaults) string { return d.FlareSolverrURL },
	),
	KeyFlareSolverrTimeout: intTunable(
		KeyFlareSolverrTimeout, "seconds", flareSolverrTimeoutMin, flareSolverrTimeoutMax,
		func(d Defaults) int { return d.FlareSolverrTimeout },
	),
	KeyFlareSolverrSessionName: stringTunable(
		KeyFlareSolverrSessionName, "text",
		func(d Defaults) string { return d.FlareSolverrSessionName },
	),
	KeyFlareSolverrSessionTTL: intTunable(
		KeyFlareSolverrSessionTTL, "minutes", flareSolverrSessionTTLMin, flareSolverrSessionTTLMax,
		func(d Defaults) int { return d.FlareSolverrSessionTTL },
	),
	KeyFlareSolverrResponseFallback: boolTunable(
		KeyFlareSolverrResponseFallback,
		func(d Defaults) bool { return d.FlareSolverrResponseFallback },
	),
	KeyNotificationsEnabled: boolTunable(
		KeyNotificationsEnabled,
		func(d Defaults) bool { return d.NotificationsEnabled },
	),
	// The engine SOCKS-proxy group (mirrors the FlareSolverr group's pattern —
	// see the KeyEngineSocks* doc comments above).
	KeyEngineSocksEnabled: boolTunable(
		KeyEngineSocksEnabled,
		func(d Defaults) bool { return d.EngineSocksEnabled },
	),
	KeyEngineSocksHost: stringTunable(
		KeyEngineSocksHost, "text",
		func(d Defaults) string { return d.EngineSocksHost },
	),
	KeyEngineSocksPort: intTunable(
		KeyEngineSocksPort, "port", engineSocksPortMin, engineSocksPortMax,
		func(d Defaults) int { return d.EngineSocksPort },
	),
	KeyEngineSocksVersion: intEnumTunable(
		KeyEngineSocksVersion, "version", engineSocksVersions,
		func(d Defaults) int { return d.EngineSocksVersion },
	),
	KeyReportingRetentionDays: intTunable(
		KeyReportingRetentionDays, "days", reportingRetentionMin, reportingRetentionMax,
		func(d Defaults) int { return d.ReportingRetentionDays },
	),
	KeyRetainedVersions: intTunable(
		KeyRetainedVersions, "count", retainedVersionsMin, retainedVersionsMax,
		func(d Defaults) int { return d.RetainedVersions },
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

// intEnumTunable builds an int-typed tunable that accepts only one of a fixed
// set of allowed values — unlike intTunable's contiguous [lo,hi] range. Used
// for the SOCKS proxy version, which is valid only as 4 or 5.
func intEnumTunable(key, unit string, allowed []int, def func(Defaults) int) tunable {
	return tunable{
		key:  key,
		typ:  TypeInt,
		unit: unit,
		validate: func(raw string) (string, error) {
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid integer", ErrInvalidSetting, key, raw)
			}
			for _, a := range allowed {
				if n == a {
					return strconv.Itoa(n), nil
				}
			}
			return "", fmt.Errorf("%w: %s must be one of %v (got %d)", ErrInvalidSetting, key, allowed, n)
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

// stringTunable builds a free-text string-typed tunable: the only
// normalisation is trimming surrounding whitespace, with no further format
// constraint. Used for values Suwayomi itself treats as an opaque string
// (e.g. the FlareSolverr session name — any string, including "", is legal).
func stringTunable(key, unit string, def func(Defaults) string) tunable {
	return tunable{
		key:  key,
		typ:  TypeString,
		unit: unit,
		validate: func(raw string) (string, error) {
			return strings.TrimSpace(raw), nil
		},
		def: func(d Defaults) string { return def(d) },
	}
}

// urlOrBlankTunable builds a string-typed tunable that accepts either an
// empty value (cleared / not configured) or a well-formed absolute http(s)
// URL, sharing the urlx.IsAbsoluteHTTP kernel with the Suwayomi settings
// proxy + extension-repo validators (§2 DRY — "valid absolute http(s) URL" is
// defined in exactly one place across the whole backend).
func urlOrBlankTunable(key, unit string, def func(Defaults) string) tunable {
	return tunable{
		key:  key,
		typ:  TypeString,
		unit: unit,
		validate: func(raw string) (string, error) {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return "", nil
			}
			if !urlx.IsAbsoluteHTTP(trimmed) {
				return "", fmt.Errorf("%w: %s must be blank or a valid absolute http(s) URL", ErrInvalidSetting, key)
			}
			return trimmed, nil
		},
		def: func(d Defaults) string { return def(d) },
	}
}
