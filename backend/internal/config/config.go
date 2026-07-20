// Package config is the sole environment boundary for the Tsundoku backend.
// Only this package reads os.Getenv; everything else receives a typed *Config.
//
// Priority order (highest wins): environment variables → config.yaml → built-in
// defaults. Environment variables use the TSUNDOKU_ prefix with "_" as the
// nested-key separator (e.g. TSUNDOKU_DATABASE_PASSWORD).
//
// Load() calls validate() before returning, so an insecure or incomplete
// configuration is rejected at startup (fail-closed semantics).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// Config is the root configuration type. Every subsystem receives a copy of
// the relevant sub-struct, never the raw environment.
type Config struct {
	// Server holds HTTP server settings.
	Server ServerConfig
	// Database holds PostgreSQL connection parameters.
	Database DatabaseConfig
	// Auth holds HMAC signing settings for the owner JWT layer.
	Auth AuthConfig
	// Storage holds library-path settings for downloaded chapters.
	Storage StorageConfig
	// Jobs holds background-job scheduler settings.
	Jobs JobsConfig
	// Health holds M7 source-health computation settings.
	Health HealthConfig
	// Sources holds the source-politeness circuit-breaker + delay defaults.
	Sources SourcesConfig
	// Metadata holds the Phase-1 native metadata engine's provider credentials.
	Metadata MetadataConfig
	// Tracker holds the Phase-3 tracker OAuth subsystem's credentials.
	Tracker TrackerConfig
	// Engine holds connection settings for the engine-host (the Kotlin/Mihon
	// process replacing Suwayomi as the download engine — Suwayomi-removal
	// P2 migration).
	Engine EngineConfig
	// Extensions holds source-extension management settings.
	Extensions ExtensionsConfig
}

// AuthConfig holds HMAC signing settings for the single-owner auth layer.
type AuthConfig struct {
	// Secret is the HMAC-SHA256 signing key for owner JWTs.
	// Must be at least 16 characters; validate() fails closed on startup when
	// this is empty or shorter, preventing all tokens from being forgeable.
	// Set via TSUNDOKU_AUTH_SECRET.
	Secret string
	// CookieSecure sets the Secure flag on the session cookie. Default true.
	// Set false ONLY for plain-HTTP LAN deploys — browsers drop Secure cookies
	// over http://, which would prevent login. Prefer fronting with TLS.
	// Set via TSUNDOKU_AUTH_COOKIESECURE.
	CookieSecure bool
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the TCP port the HTTP server listens on (e.g. "9833").
	Port string
}

// DatabaseConfig holds PostgreSQL connection parameters.
type DatabaseConfig struct {
	// Host is the PostgreSQL server hostname or IP address.
	Host string
	// Port is the PostgreSQL server port (default "5432").
	Port string
	// User is the database user name.
	User string
	// Password is the database user password. Required — validate() fails
	// closed when this is empty.
	Password string
	// Name is the database (schema) name.
	Name string
	// SSLMode is the PostgreSQL SSL mode (e.g. "disable", "require",
	// "verify-full"). Default is "disable" for local dev.
	SSLMode string
}

// DSN returns the PostgreSQL connection URI for this database config.
// net/url is used so that a password containing @ / # % is percent-encoded,
// producing a valid URI that pgx and lib/pq can parse correctly.
func (d DatabaseConfig) DSN() string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.User, d.Password),
		Host:     d.Host + ":" + d.Port,
		Path:     "/" + d.Name,
		RawQuery: "sslmode=" + url.QueryEscape(d.SSLMode),
	}
	return u.String()
}

// JobsConfig holds background-job scheduler settings.
type JobsConfig struct {
	// DownloadInterval is the tick period for the download runner (queue drain
	// + upgrade-swap). Default 15m. Set via TSUNDOKU_JOBS_DOWNLOADINTERVAL.
	DownloadInterval time.Duration

	// DownloadConcurrency bounds how many chapter downloads run in parallel PER
	// SOURCE in the dispatcher (each is a live upstream fetch), and how many of a
	// source's queued chapters may be "downloading" at once. Default 5 (Kaizoku
	// parity). This is the env-sourced DEFAULT the runtime settings overlay
	// (jobs.download_concurrency) can override without a restart — the dispatcher
	// reads the tunable per cycle, not this value directly. validate() rejects a
	// value below 1. Set via TSUNDOKU_JOBS_DOWNLOADCONCURRENCY.
	DownloadConcurrency int

	// RefreshInterval is the tick period for the M5 discovery poll, which
	// re-fetches every monitored series' chapter list to find new releases.
	// Default 2h (Kaizoku.GO's proven per-title cadence). Set via
	// TSUNDOKU_JOBS_REFRESHINTERVAL.
	RefreshInterval time.Duration

	// RefreshConcurrency bounds how many provider re-fetches the refresh sweep
	// runs in parallel (each is a live upstream call). Default 4 — gentler on
	// sources than the in-process search fan-out. Set via
	// TSUNDOKU_JOBS_REFRESHCONCURRENCY.
	RefreshConcurrency int

	// MaxRetries is the PER-SOURCE retry budget: how many times a chapter is
	// retried from ONE source before that source is abandoned for it. EVERY fetch
	// failure counts toward it (Kaizoku-style "count every retry, terminal at max"
	// model); a chapter is parked in permanently_failed only once every source
	// offering it is exhausted. Default 5. This is the env-sourced DEFAULT the
	// runtime settings overlay (internal/settings) can override per-deployment
	// without a restart. Set via TSUNDOKU_JOBS_MAXRETRIES.
	MaxRetries int

	// RetryBackoff is the FLAT delay before every retry of a failed chapter from a
	// source: the gap between successive tries is constant (no per-attempt growth).
	// Default 30m. Like MaxRetries it is the env default behind the runtime
	// settings overlay. Set via TSUNDOKU_JOBS_RETRYBACKOFF.
	RetryBackoff time.Duration

	// ExtensionCheckInterval is the tick period for the extension auto-check job,
	// which calls FetchExtensions and broadcasts the count of available updates via
	// an extensions.checked SSE event. Default 24h. Set to 0 to disable the job
	// (no extensions are checked until a non-zero interval is configured at
	// runtime via the settings API). Set via TSUNDOKU_JOBS_EXTENSIONCHECKINTERVAL.
	ExtensionCheckInterval time.Duration

	// WarmupInterval is the tick period for the anti-bot session warm-up job,
	// which keeps slow (Cloudflare-protected) sources warm with a cheap Browse
	// call so interactive search stays fast. Default 15m (comfortably under a
	// FlareSolverr challenge-clearance TTL). Set to 0 to disable the job. This is
	// the env DEFAULT the runtime settings overlay can override without a restart.
	// Set via TSUNDOKU_JOBS_WARMUPINTERVAL.
	WarmupInterval time.Duration

	// WarmupSlowThresholdMs is the EWMA-latency threshold (milliseconds) above
	// which a source is considered "slow" and warmed by the WarmSlow pass. Default
	// 5000. Like WarmupInterval it is the env default behind the runtime settings
	// overlay. Set via TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS.
	WarmupSlowThresholdMs int

	// SearchCacheTTL is the lifetime of a cached INTERACTIVE Search fan-out result
	// (the heaviest anti-bot amplifier). Default 1h. This is the env DEFAULT behind
	// the runtime settings overlay (jobs.search_cache_ttl); the search path reads
	// the tunable per Get (hot reload) and 0 disables the cache. Set via
	// TSUNDOKU_JOBS_SEARCHCACHETTL.
	SearchCacheTTL time.Duration

	// ChapterCacheTTL is the lifetime of a cached FetchChapters result for the
	// interactive coverage→configure→adopt discovery flow. Default 1h. Env DEFAULT
	// behind the runtime settings overlay (jobs.chapter_cache_ttl), read per Get
	// (hot reload); 0 disables the cache. The refresh sweep does NOT use this cache
	// (it fetches fresh each sweep), so a long TTL here never stales-out discovery.
	// Set via TSUNDOKU_JOBS_CHAPTERCACHETTL.
	ChapterCacheTTL time.Duration

	// SuppressSplitParts enables fractional-part suppression (skip N.1..N.x when
	// the whole N is downloaded). Set via TSUNDOKU_JOBS_SUPPRESSSPLITPARTS.
	SuppressSplitParts bool

	// TrackRetryInterval is the tick period for the tracker-push retry-queue
	// drain job (internal/tracker/retry + job.Runner.StartTrackerRetry).
	// Default 5m. This is the env-sourced DEFAULT the runtime settings overlay
	// (jobs.track_retry_interval) can override without a restart; always-on
	// (no 0-disables sentinel, unlike WarmupInterval/ExtensionCheckInterval —
	// the retry queue must keep draining). Set via
	// TSUNDOKU_JOBS_TRACKRETRYINTERVAL.
	TrackRetryInterval time.Duration

	// AutoUpdateTrack gates the reading-triggered tracker-sync push (spec/
	// trackers-sync-phase4 §2): when true (the default), marking a chapter
	// read in the in-app reader fires a best-effort progress push to every
	// bound tracker. This is the env-sourced DEFAULT the runtime settings
	// overlay (trackers.auto_update_track) can override without a restart.
	// Set via TSUNDOKU_JOBS_AUTOUPDATETRACK.
	AutoUpdateTrack bool
}

// SourcesConfig holds the env-sourced DEFAULTS for the source-politeness
// circuit-breaker + politeness delay (internal/sourcegate) — the runtime
// settings overlay (jobs sources.*) can override each without a restart; the
// dispatcher/refresh/warm-up gate reads the tunable at use-time, not this
// struct directly.
type SourcesConfig struct {
	// FailureThreshold is how many CONSECUTIVE failures against one physical
	// source trip its circuit-breaker into cooldown. Default 5. validate()
	// rejects a value below 1 (a source must always get at least one try).
	// Set via TSUNDOKU_SOURCES_FAILURETHRESHOLD.
	FailureThreshold int
	// Cooldown is how long a tripped source's circuit-breaker stays open before
	// it is available again. Default 30m. validate() rejects a value below 1m.
	// Set via TSUNDOKU_SOURCES_COOLDOWN.
	Cooldown time.Duration
	// MinRequestDelay is the minimum politeness delay enforced between
	// successive requests to the SAME physical source, independent of the
	// per-source concurrency cap. Default 500ms (Kaizoku.GO parity); 0 disables
	// it. validate() rejects a negative value. Set via
	// TSUNDOKU_SOURCES_MINREQUESTDELAY.
	MinRequestDelay time.Duration
}

// HealthConfig tunes the M7 source-health computation.
type HealthConfig struct {
	// StaleGraceDays is how old a source's newest chapter must be — on top of
	// the source having fallen behind the series' leading edge — before it is
	// reported "stale". Default 14. Set via TSUNDOKU_HEALTH_STALEGRACEDAYS.
	StaleGraceDays int
}

// MetadataConfig holds credentials for the Phase-1 native metadata engine's
// public-read providers (internal/metadata/providers). AniList, MangaDex,
// MangaUpdates, and Kitsu are all fully public — only MyAnimeList requires a
// registered app client-id header. MAL is therefore OPTIONAL: an empty
// MALClientID simply leaves the MAL provider unable to answer (its client
// returns an error per-call, logged+skipped by the registry's fan-out — see
// internal/metadata.Registry.Search/Identify), never a startup failure. The
// other four providers carry the engine end-to-end without it.
type MetadataConfig struct {
	// MALClientID is MyAnimeList's required app credential, sent as the
	// X-MAL-CLIENT-ID header. Default "" (MAL disabled until configured).
	// Set via TSUNDOKU_METADATA_MAL_CLIENTID.
	//
	// GOTCHA — this is the only field in Config with an explicit `koanf` struct
	// tag. envKeyTransform only turns the FIRST "_" after the TSUNDOKU_ prefix
	// into a "." (splitting the top-level struct key from the remainder); every
	// other field's env suffix happens to be underscore-free (e.g.
	// DOWNLOADURLTEMPLATE), so the untagged default — koanf/mapstructure
	// case-insensitively matching the map key against the Go field name — just
	// works. MAL_CLIENTID keeps its inner "_" (the transform only strips the
	// first one), so it resolves to the nested key "mal_clientid" — which does
	// NOT case-insensitively equal "MALClientID" without a tag pinning the
	// match explicitly.
	MALClientID string `koanf:"mal_clientid"`
	// AutoIdentify gates the background auto-identify pass (spec/metadata-
	// engine-phase1 §4): when true (the default), a freshly adopted/imported
	// series is automatically matched + merged against the registered
	// providers. This is the env-sourced DEFAULT the runtime settings overlay
	// (metadata.auto_identify) can override without a restart — see
	// internal/settings' KeyMetadataAutoIdentify. Set via
	// TSUNDOKU_METADATA_AUTOIDENTIFY.
	AutoIdentify bool
}

// TrackerConfig holds credentials for the Phase-3 tracker OAuth subsystem's
// two OAuth trackers (AniList, MAL — see internal/tracker/providers). This
// is a SEPARATE credential set from MetadataConfig: metadata is public-read
// (only MAL needs a client-id header, no login); trackers are OAuth-login
// (spec/trackers-and-rich-library-umbrella-v2 §1 — the two subsystems share
// a physical provider, never a config struct).
//
// Every field DEFAULTS BLANK, and validate() does NOT fail on blank — the
// fleet "blank disables" pattern: an unconfigured tracker's AuthURL simply
// fails closed with
// tracker.ErrClientIDNotConfigured (that tracker's connect button stays
// hidden/disabled in the UI), and a blank PublicURL fails closed with
// connect.ErrPublicURLNotConfigured — the whole subsystem is DORMANT until
// the owner activates it (spec/trackers-oauth-phase3 §2), never a startup
// failure.
type TrackerConfig struct {
	// AniListClientID is AniList's registered app client id, sent as the
	// implicit-grant authorize URL's client_id parameter. Default ""
	// (AniList tracker connect disabled until configured). Set via
	// TSUNDOKU_TRACKER_ANILISTCLIENTID.
	AniListClientID string `koanf:"anilistclientid"`
	// MALClientID is MyAnimeList's registered app client id for the
	// TRACKER (auth-code+PKCE) OAuth flow — may reuse the same app as
	// MetadataConfig.MALClientID (a metadata-only app's client-id also
	// works for OAuth as long as its registered redirect list includes
	// this instance's callback) or a dedicated one; this package never
	// assumes either. Default "" (MAL tracker connect disabled until
	// configured). Set via TSUNDOKU_TRACKER_MALCLIENTID.
	//
	// Explicit koanf tags on both client-id fields (this one and
	// AniListClientID) pin the env-key match EXPLICITLY rather than
	// relying on koanf/mapstructure's default case-insensitive matching —
	// the same defensive discipline as MetadataConfig.MALClientID's
	// `mal_clientid` tag (see its doc comment for the underscore-collision
	// footgun that fix guards against), applied here even though the
	// current env-var spelling (TRACKER_MALCLIENTID, no inner underscore)
	// does not itself hit that collision, so a future rename can't
	// silently reintroduce it.
	MALClientID string `koanf:"malclientid"`
	// MALClientSecret is MyAnimeList's registered app CLIENT SECRET for the
	// tracker OAuth flow. Default "" — a blank secret means this is a
	// PUBLIC/"other"-type MAL app (PKCE alone is sufficient; no secret is
	// ever sent to the token endpoint, preserving the prior behavior).
	// CONFIRMED-in-production: a CONFIDENTIAL MAL app (the common "web"
	// registration type most owners create) rejects the token exchange with
	// `401 invalid_client "Client authentication failed"` unless
	// client_secret rides ALONG WITH PKCE — MAL's confidential-client check
	// is independent of PKCE, so both are required together for that app
	// type. internal/tracker/mal.Client sends client_secret ONLY when this
	// is non-blank, so a public app's request shape is byte-for-byte
	// unchanged. Set via TSUNDOKU_TRACKER_MALCLIENTSECRET. Explicit koanf
	// tag for the same reason as MALClientID/AniListClientID above.
	MALClientSecret string `koanf:"malclientsecret"`
	// PublicURL is this instance's own public base URL — the redirect
	// base every tracker's OAuth app must have "${PublicURL}/auth/tracker/
	// callback" registered as its redirect_uri (spec §2 — direct instance
	// redirect, no Cloudflare relay). Default "" (AuthURL fails closed
	// with connect.ErrPublicURLNotConfigured until set). When non-blank it
	// must be a well-formed absolute http/https URL; validate() fails
	// closed otherwise — a malformed public URL would build a redirect_uri
	// no provider could ever call back to. Set via
	// TSUNDOKU_TRACKER_PUBLICURL.
	PublicURL string `koanf:"publicurl"`
}

// EngineConfig holds connection settings for the engine-host — the Kotlin/
// Mihon process that replaces Suwayomi as the download engine. The engine
// host is launched by the container entrypoint, not this process; Tsundoku
// only ever talks to it as an HTTP client (see internal/sourceengine for the
// typed client).
type EngineConfig struct {
	// URL is the engine-host's base HTTP address (e.g. "http://localhost:7777",
	// no trailing slash required — sourceengine.New trims one if present).
	// Defaults to "http://localhost:7777" (the engine-host's own default
	// listen port, confirmed against engine-host/src/main/kotlin/enginehost/
	// Main.kt's TSUNDOKU_ENGINE_PORT fallback). When non-empty it must be a
	// well-formed absolute http/https URL — validate() fails closed
	// otherwise, mirroring Tracker.PublicURL. Set via TSUNDOKU_ENGINE_URL.
	URL string
	// HTTPTimeout is the deadline applied to every request the engine-host API
	// client makes (search/browse/details/chapters/pages/image). The default is
	// deliberately generous (3m) because a page-image fetch forces the engine
	// host to contact the upstream source, which is legitimately slow — under
	// per-provider concurrency a shorter deadline caused frequent
	// "context deadline exceeded (Client.Timeout exceeded while awaiting
	// headers)" failures. Default 3m. validate() rejects a non-positive value.
	// Set via TSUNDOKU_ENGINE_HTTPTIMEOUT (formerly TSUNDOKU_SUWAYOMI_HTTPTIMEOUT
	// — moved off the retired SuwayomiConfig in the P2 Suwayomi-removal slice
	// that deleted internal/suwayomi).
	HTTPTimeout time.Duration
	// SearchTimeout is the OVERALL deadline for one interactive multi-source
	// search fan-out (imports.Service.Search), DISTINCT from HTTPTimeout (the
	// per-request client deadline used by ALL API calls, including background
	// downloads). Interactive search fans out to every installed source in
	// parallel; a Cloudflare-protected source can hang for a long time solving
	// an anti-bot challenge, and those challenges are solved serially through a
	// single embedded WebView, so a few slow sources can stall the whole
	// response. This deadline bounds the response and yields PARTIAL results
	// (the sources that answered in time) instead of hanging.
	//
	// The default is 85s: comfortably UNDER a CDN edge's ~100s cut-off (e.g.
	// Cloudflare's 524 timeout) so the user gets partial results rather than a
	// 524, while still allowing a cold anti-bot source ~30–60s to solve its
	// challenge and contribute. Downloads deliberately keep the generous 3m
	// HTTPTimeout — this knob only governs the interactive search fan-out.
	// Default 85s. validate() rejects a non-positive value.
	// Set via TSUNDOKU_ENGINE_SEARCHTIMEOUT (formerly
	// TSUNDOKU_SUWAYOMI_SEARCHTIMEOUT).
	SearchTimeout time.Duration
	// RuntimeDir is the directory the extension-.apk byte cache (internal/
	// enginetopo/apkcache) is rooted under — a Tsundoku-owned durable cache of
	// engine-topology artifacts, NOT an engine-host data directory (Tsundoku
	// launches no engine-host process of its own). Set via
	// TSUNDOKU_ENGINE_RUNTIMEDIR (formerly TSUNDOKU_SUWAYOMI_RUNTIMEDIR — the
	// default path is unchanged so an existing deploy's volume mount keeps
	// working; only the env var name moved).
	RuntimeDir string
	// HostBin is the filesystem path to the engine-host launcher binary that the
	// per-profile process launcher (internal/enginehost) spawns one JVM of per
	// non-default network profile. Default "/app/engine-host/bin/tsundoku-engine-
	// host" — the path the container image installs it at (see the Dockerfile).
	// This is NOT used for the DEFAULT engine-host instance (the container
	// entrypoint launches that one directly); it is only read when a source is
	// bound to a non-default network profile and therefore needs its own
	// instance. DELIBERATELY NOT fail-closed-validated: a deploy with no network
	// bindings must boot even if this path does not exist locally (it is only
	// dereferenced when a profile actually spawns, and a spawn failure degrades
	// that profile to the default instance — the zero-disruption invariant). Set
	// via TSUNDOKU_ENGINE_HOSTBIN.
	HostBin string
	// DataDir is the engine-host data-root the container entrypoint uses for the
	// DEFAULT instance; the per-profile launcher puts each non-default instance's
	// own data dir under "<DataDir>/profiles/<hash>". Default "/config/engine" —
	// the SAME value TSUNDOKU_ENGINE_DATA carries for the entrypoint, so both the
	// default instance and the launched profiles live on the one persistent
	// volume. Bound to the SAME env var the entrypoint reads
	// (TSUNDOKU_ENGINE_DATA) via the `koanf:"data"` tag, so there is exactly one
	// data-root knob. Like HostBin, deliberately NOT fail-closed-validated (see
	// its doc). Set via TSUNDOKU_ENGINE_DATA.
	DataDir string `koanf:"data"`
	// KCEFBundle is the path to the pre-downloaded KCEF (Chromium) runtime the
	// image bakes in, which the per-profile launcher symlinks into each profile's
	// "<dataDir>/bin/kcef" so a spawned instance needs no first-run Chromium
	// download (mirroring what entrypoint.sh does for the default instance via
	// ENGINE_KCEF_BUNDLE). Default "/app/kcef-runtime/bin/kcef". When it is blank
	// or the path does not exist the launcher simply skips KCEF seeding (best-
	// effort — a missing bundle degrades a WebView source, never the spawn). Like
	// HostBin, deliberately NOT fail-closed-validated. Set via
	// TSUNDOKU_ENGINE_KCEFBUNDLE.
	KCEFBundle string
}

// ExtensionsConfig holds source-extension management settings.
type ExtensionsConfig struct {
	// RetainedVersions is how many .apk versions per extension the apk cache
	// keeps (the reversible-update rollback-history depth). Default 3. This is
	// the env-sourced DEFAULT the runtime settings overlay
	// (extensions.retained_versions) can override without a restart; the prune
	// reads the tunable at use-time, not this value directly. validate() rejects
	// a value outside [1, 20]. Set via TSUNDOKU_EXTENSIONS_RETAINEDVERSIONS.
	RetainedVersions int
}

// StorageConfig holds library-path settings.
type StorageConfig struct {
	// Folder is the absolute path to the manga library on disk where
	// downloaded chapters are stored.
	Folder string
}

// defaults returns the built-in default values for all config keys.
// These are overridden by config.yaml and then by environment variables.
func defaults() map[string]any {
	return map[string]any{
		"server.port":       "9833",
		"database.host":     "127.0.0.1",
		"database.port":     "5432",
		"database.user":     "tsundoku",
		"database.password": "",
		"database.name":     "tsundoku",
		"database.sslmode":  "disable",
		"auth.secret":       "",
		"auth.cookiesecure": true,
		// Jobs — background-job scheduler.
		"jobs.downloadinterval":       "15m",
		"jobs.downloadconcurrency":    5,
		"jobs.refreshinterval":        "2h",
		"jobs.refreshconcurrency":     4,
		"jobs.maxretries":             5,
		"jobs.retrybackoff":           "30m",
		"jobs.extensioncheckinterval": "24h",
		"jobs.warmupinterval":         "15m",
		"jobs.warmupslowthresholdms":  5000,
		"jobs.searchcachettl":         "1h",
		"jobs.chaptercachettl":        "1h",
		"jobs.suppresssplitparts":     true,
		"jobs.trackretryinterval":     "5m",
		"jobs.autoupdatetrack":        true,
		// Health — M7 source-health computation.
		"health.stalegracedays": 14,
		"storage.folder":        "/data/manga",
		// Sources — source-politeness circuit-breaker + delay defaults.
		"sources.failurethreshold": 5,
		"sources.cooldown":         "30m",
		"sources.minrequestdelay":  "500ms",
		// Metadata — Phase-1 native metadata engine provider credentials.
		"metadata.mal_clientid": "",
		"metadata.autoidentify": true,
		// Tracker — Phase-3 tracker OAuth subsystem credentials. All blank
		// ⇒ the whole subsystem is dormant (see TrackerConfig's doc comment).
		"tracker.anilistclientid": "",
		"tracker.malclientid":     "",
		"tracker.malclientsecret": "",
		"tracker.publicurl":       "",
		// Engine — the engine-host connection (Suwayomi-removal P2).
		"engine.url":           "http://localhost:7777",
		"engine.httptimeout":   "3m",
		"engine.searchtimeout": "85s",
		"engine.runtimedir":    "/data/suwayomi",
		// Engine — the per-profile process launcher (internal/enginehost). None of
		// these three is fail-closed-validated: a deploy with no network bindings
		// must boot even if the paths do not exist locally (they are only
		// dereferenced when a source's non-default profile actually spawns).
		"engine.hostbin":    "/app/engine-host/bin/tsundoku-engine-host",
		"engine.data":       "/config/engine",
		"engine.kcefbundle": "/app/kcef-runtime/bin/kcef",
		// Extensions — source-extension management.
		"extensions.retainedversions": 3,
	}
}

// Load reads configuration from (in ascending priority):
//  1. built-in defaults,
//  2. config.yaml in the current working directory (optional — missing file is
//     silently ignored),
//  3. environment variables prefixed with TSUNDOKU_ using "_" as the nested
//     key separator.
//
// It returns the populated Config and the result of validate(). A non-nil error
// means the binary should refuse to start.
func Load() (*Config, error) {
	k := koanf.New(".")

	// Layer 1: built-in defaults.
	// UNCOVERABLE: confmap.Provider with a static map[string]any never errors.
	if err := k.Load(confmap.Provider(defaults(), "."), nil); err != nil {
		return nil, fmt.Errorf("config: load defaults: %w", err)
	}

	// Layer 2: optional config.yaml — silently skip if absent.
	fp := file.Provider("config.yaml")
	if err := k.Load(fp, yaml.Parser()); err != nil {
		// Only ignore "no such file" — surface real parse errors.
		if !isNotExist(err) {
			return nil, fmt.Errorf("config: parse config.yaml: %w", err)
		}
	}

	// Layer 3: environment variables — TSUNDOKU_ prefix, "_" nested separator.
	// The callback receives the full key (e.g. "TSUNDOKU_DATABASE_PASSWORD");
	// we strip the prefix and transform to a dot-delimited koanf key.
	// UNCOVERABLE: env.Provider reads os.Environ which never errors.
	if err := k.Load(env.Provider("TSUNDOKU_", ".", envKeyTransform), nil); err != nil {
		return nil, fmt.Errorf("config: load env: %w", err)
	}

	cfg := &Config{}
	// UNCOVERABLE: UnmarshalWithConf against a correct struct type never errors.
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	return cfg, cfg.validate()
}

// envKeyTransform converts a full environment variable name in the form
// TSUNDOKU_DATABASE_PASSWORD into the dot-delimited koanf key
// "database.password". The env.Provider passes the FULL key (including
// the TSUNDOKU_ prefix) to this callback — we must strip it here.
//
// Concrete mapping:
//
//	TSUNDOKU_SERVER_PORT                    → server.port
//	TSUNDOKU_DATABASE_HOST                  → database.host
//	TSUNDOKU_DATABASE_PASSWORD              → database.password
//	TSUNDOKU_DATABASE_SSLMODE               → database.sslmode
//	TSUNDOKU_AUTH_SECRET                    → auth.secret
//	TSUNDOKU_AUTH_COOKIESECURE              → auth.cookiesecure
//	TSUNDOKU_JOBS_DOWNLOADINTERVAL          → jobs.downloadinterval
//	TSUNDOKU_JOBS_DOWNLOADCONCURRENCY       → jobs.downloadconcurrency
//	TSUNDOKU_JOBS_REFRESHINTERVAL           → jobs.refreshinterval
//	TSUNDOKU_JOBS_REFRESHCONCURRENCY        → jobs.refreshconcurrency
//	TSUNDOKU_JOBS_MAXRETRIES                → jobs.maxretries
//	TSUNDOKU_JOBS_RETRYBACKOFF              → jobs.retrybackoff
//	TSUNDOKU_JOBS_EXTENSIONCHECKINTERVAL    → jobs.extensioncheckinterval
//	TSUNDOKU_JOBS_WARMUPINTERVAL            → jobs.warmupinterval
//	TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS     → jobs.warmupslowthresholdms
//	TSUNDOKU_JOBS_SEARCHCACHETTL            → jobs.searchcachettl
//	TSUNDOKU_JOBS_CHAPTERCACHETTL           → jobs.chaptercachettl
//	TSUNDOKU_JOBS_TRACKRETRYINTERVAL        → jobs.trackretryinterval
//	TSUNDOKU_JOBS_AUTOUPDATETRACK           → jobs.autoupdatetrack
//	TSUNDOKU_STORAGE_FOLDER                 → storage.folder
//	TSUNDOKU_SOURCES_FAILURETHRESHOLD       → sources.failurethreshold
//	TSUNDOKU_SOURCES_COOLDOWN               → sources.cooldown
//	TSUNDOKU_SOURCES_MINREQUESTDELAY        → sources.minrequestdelay
//	TSUNDOKU_METADATA_MAL_CLIENTID          → metadata.mal_clientid (see the
//	                                          `koanf:"mal_clientid"` tag on
//	                                          MetadataConfig.MALClientID)
//	TSUNDOKU_METADATA_AUTOIDENTIFY           → metadata.autoidentify
//	TSUNDOKU_TRACKER_ANILISTCLIENTID        → tracker.anilistclientid
//	TSUNDOKU_TRACKER_MALCLIENTID            → tracker.malclientid
//	TSUNDOKU_TRACKER_PUBLICURL              → tracker.publicurl
//	TSUNDOKU_ENGINE_URL                     → engine.url
//	TSUNDOKU_ENGINE_HTTPTIMEOUT             → engine.httptimeout
//	TSUNDOKU_ENGINE_SEARCHTIMEOUT           → engine.searchtimeout
//	TSUNDOKU_ENGINE_RUNTIMEDIR              → engine.runtimedir
//	TSUNDOKU_ENGINE_HOSTBIN                 → engine.hostbin
//	TSUNDOKU_ENGINE_DATA                    → engine.data (see the
//	                                          `koanf:"data"` tag on
//	                                          EngineConfig.DataDir)
//	TSUNDOKU_ENGINE_KCEFBUNDLE              → engine.kcefbundle
//
// Convention: after stripping the prefix the first "_" separates the
// top-level struct key from the field name; the remainder is kept as-is
// (lowercased). This matches the flat field names in the struct definitions.
func envKeyTransform(s string) string {
	const prefix = "TSUNDOKU_"
	s = strings.TrimPrefix(s, prefix)
	// Lowercase and replace the first underscore with "." to build the
	// koanf dotted path (e.g. DATABASE_PASSWORD → database.password).
	return strings.Replace(strings.ToLower(s), "_", ".", 1)
}

// isNotExist reports whether err is a "file not found" error.
// It uses errors.Is against os.ErrNotExist rather than string-matching so that
// it works correctly on all platforms and with wrapped errors.
func isNotExist(err error) bool { return err != nil && errors.Is(err, os.ErrNotExist) }

// minAuthSecretLen is the minimum acceptable length for the HMAC auth secret.
// A shorter secret makes tokens trivially forgeable; we fail closed at startup.
const minAuthSecretLen = 16

// validate checks that the loaded configuration is safe to use. It returns an
// error at startup rather than allowing the binary to run with a broken or
// insecure setup (fail-closed semantics per DEC-NX-054 / QCAT-019).
//
// Rules enforced:
//   - Database.Password must be set — never run against a passwordless DB.
//   - Auth.Secret must be at least 16 characters — an empty or short HMAC
//     secret makes all tokens forgeable (flagged by Task 5 adversarial review).
//   - Engine.HTTPTimeout must be positive — a zero/negative client deadline
//     would either never time out or reject every request.
//   - Engine.SearchTimeout must be positive — a zero/negative deadline would
//     either never bound the interactive search fan-out or reject it outright.
//   - Jobs.DownloadConcurrency must be at least 1 — a non-positive per-provider
//     concurrency would stall the dispatcher entirely.
//   - Jobs.WarmupSlowThresholdMs must be at least 1 — a non-positive slow
//     threshold would flag every source slow on every warm pass.
//   - Sources.FailureThreshold must be at least 1 — a source must always get
//     at least one try before its circuit-breaker can trip.
//   - Sources.Cooldown must be at least 1 minute — an effectively-zero cooldown
//     would let a tripped source re-trip immediately, defeating the breaker.
//   - Sources.MinRequestDelay must not be negative — a negative politeness
//     delay is meaningless (0 is the valid "disabled" sentinel).
//   - Tracker.PublicURL, when set, must be a well-formed absolute http/https
//     URL — blank leaves the whole tracker OAuth subsystem dormant and is
//     NOT an error (see TrackerConfig's doc comment).
//   - Engine.URL, when non-empty, must be a well-formed absolute http/https
//     URL — the default is always valid, so this only fires on an operator
//     override (see EngineConfig's doc comment).
func (c *Config) validate() error {
	var errs []string

	if c.Database.Password == "" {
		errs = append(errs, "TSUNDOKU_DATABASE_PASSWORD must be set")
	}

	if len(c.Auth.Secret) < minAuthSecretLen {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_AUTH_SECRET must be at least %d characters (got %d)",
			minAuthSecretLen, len(c.Auth.Secret),
		))
	}

	if c.Engine.HTTPTimeout <= 0 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_ENGINE_HTTPTIMEOUT must be positive (got %s)", c.Engine.HTTPTimeout,
		))
	}

	if c.Engine.SearchTimeout <= 0 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_ENGINE_SEARCHTIMEOUT must be positive (got %s)", c.Engine.SearchTimeout,
		))
	}

	if c.Jobs.DownloadConcurrency < 1 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_JOBS_DOWNLOADCONCURRENCY must be at least 1 (got %d)", c.Jobs.DownloadConcurrency,
		))
	}

	if c.Jobs.WarmupSlowThresholdMs < 1 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS must be at least 1 (got %d)", c.Jobs.WarmupSlowThresholdMs,
		))
	}

	if c.Extensions.RetainedVersions < 1 || c.Extensions.RetainedVersions > 20 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_EXTENSIONS_RETAINEDVERSIONS must be in [1, 20] (got %d)", c.Extensions.RetainedVersions,
		))
	}

	errs = append(errs, validateSourcesConfig(c.Sources)...)
	errs = append(errs, validateTrackerConfig(c.Tracker)...)
	errs = append(errs, validateEngineConfig(c.Engine)...)

	if len(errs) > 0 {
		return errors.New("config: invalid configuration: " + strings.Join(errs, "; "))
	}
	return nil
}

// validateSourcesConfig fails closed on an invalid source-politeness
// configuration: the failure threshold must allow at least one try before a
// source's circuit-breaker can trip, the cooldown must be long enough to
// actually protect a blocked source, and the politeness delay must not be
// negative (0 is the valid "disabled" sentinel). Extracted from validate() to
// keep its cyclomatic complexity low.
func validateSourcesConfig(s SourcesConfig) []string {
	var errs []string
	if s.FailureThreshold < 1 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_SOURCES_FAILURETHRESHOLD must be at least 1 (got %d)", s.FailureThreshold,
		))
	}
	if s.Cooldown < time.Minute {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_SOURCES_COOLDOWN must be at least 1m (got %s)", s.Cooldown,
		))
	}
	if s.MinRequestDelay < 0 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_SOURCES_MINREQUESTDELAY must not be negative (got %s)", s.MinRequestDelay,
		))
	}
	return errs
}

// validateTrackerConfig fails closed on a malformed tracker public URL. A
// blank value leaves the whole tracker OAuth subsystem dormant and passes
// (the check is skipped — see TrackerConfig's doc comment); a non-blank
// value must be an absolute http/https URL with a host, mirroring
// validateEngineConfig's same rule for Engine.URL — a malformed public URL
// would build a redirect_uri no OAuth provider could ever call back to, so
// it is worth rejecting at startup rather than failing only when the owner
// first tries to connect a tracker. Extracted (mirrors validateSourcesConfig)
// to keep validate() itself under the cyclop complexity budget.
func validateTrackerConfig(t TrackerConfig) []string {
	if t.PublicURL == "" || urlx.IsAbsoluteHTTP(t.PublicURL) {
		return nil
	}
	return []string{fmt.Sprintf("TSUNDOKU_TRACKER_PUBLICURL %q must be an absolute http/https URL", t.PublicURL)}
}

// validateEngineConfig fails closed on a malformed engine-host URL. A blank
// value is accepted (the caller-facing default is always non-blank, but the
// check itself mirrors validateTrackerConfig's "blank disables" shape rather
// than special-casing on the default); a non-blank value must be an absolute
// http/https URL with a host, reusing the shared urlx kernel (§2 DRY).
func validateEngineConfig(e EngineConfig) []string {
	if e.URL == "" || urlx.IsAbsoluteHTTP(e.URL) {
		return nil
	}
	return []string{fmt.Sprintf("TSUNDOKU_ENGINE_URL %q must be an absolute http/https URL", e.URL)}
}
