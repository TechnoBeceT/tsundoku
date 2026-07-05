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
	// Suwayomi holds connection and lifecycle settings for the embedded
	// Suwayomi manga server (M2 integration).
	Suwayomi SuwayomiConfig
	// Storage holds library-path settings for downloaded chapters.
	Storage StorageConfig
	// Jobs holds background-job scheduler settings.
	Jobs JobsConfig
	// Health holds M7 source-health computation settings.
	Health HealthConfig
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

// suwayomiDefaultVersion is the pinned Suwayomi-Server release used for
// provisioning. Verified online on 2026-06-23 against
// https://github.com/Suwayomi/Suwayomi-Server/releases — update this constant
// (and the Task 1 report) when bumping to a newer release.
const suwayomiDefaultVersion = "v2.2.2100"

// suwayomiDownloadURLTemplate is the GitHub release JAR asset URL pattern.
// Both %s placeholders receive the Version tag (first = path segment, second = filename).
// Example: .../download/v2.2.2100/Suwayomi-Server-v2.2.2100.jar
// Verified against https://github.com/Suwayomi/Suwayomi-Server/releases on 2026-06-23.
const suwayomiDownloadURLTemplate = "https://github.com/Suwayomi/Suwayomi-Server/releases/download/%s/Suwayomi-Server-%s.jar"

// SuwayomiConfig holds connection and lifecycle settings for the embedded
// Suwayomi manga server. M0 fields (Host, Port, BasePath) are preserved;
// M2 adds provisioning and timeout fields.
type SuwayomiConfig struct {
	// ExternalURL selects the Suwayomi lifecycle mode (fleet "blank-disables"
	// pattern). Blank ⇒ EMBEDDED mode: tsundoku provisions, runs, and stops its
	// own Suwayomi JAR. Set ⇒ EXTERNAL mode: the client targets this HTTP base
	// URL verbatim (e.g. a homelab Suwayomi) and tsundoku owns no process. When
	// set it must be a well-formed absolute http/https URL (validate() fails
	// closed otherwise). Set via TSUNDOKU_SUWAYOMI_EXTERNALURL.
	ExternalURL string
	// Host is the Suwayomi server host (default "localhost").
	// Set via TSUNDOKU_SUWAYOMI_HOST.
	Host string
	// Port is the Suwayomi server port (default "4567").
	// Set via TSUNDOKU_SUWAYOMI_PORT.
	Port string
	// BasePath is the Suwayomi API base path (default "/api").
	// Set via TSUNDOKU_SUWAYOMI_BASEPATH.
	BasePath string
	// Version is the pinned Suwayomi-Server release tag to provision.
	// Defaults to suwayomiDefaultVersion. Set via TSUNDOKU_SUWAYOMI_VERSION.
	Version string
	// RuntimeDir is the directory where the provisioned JAR and Suwayomi
	// data are stored. Set via TSUNDOKU_SUWAYOMI_RUNTIMEDIR.
	RuntimeDir string
	// DownloadURLTemplate is the fmt-style URL pattern used to build the
	// JAR asset download URL; the two %s placeholders are both filled with
	// the Version tag. Set via TSUNDOKU_SUWAYOMI_DOWNLOADURLTEMPLATE.
	DownloadURLTemplate string
	// StartTimeout is how long to wait for Suwayomi to become ready after
	// launch. Default 2m. Set via TSUNDOKU_SUWAYOMI_STARTTIMEOUT.
	StartTimeout time.Duration
	// DownloadTimeout is the HTTP client deadline for downloading the JAR.
	// Default 10m. Set via TSUNDOKU_SUWAYOMI_DOWNLOADTIMEOUT.
	DownloadTimeout time.Duration
	// HTTPTimeout is the deadline applied to every request the Suwayomi API
	// client makes (GraphQL + page-image REST). It is DISTINCT from StartTimeout
	// (process-ready wait) and DownloadTimeout (JAR download): those govern the
	// JAR/process lifecycle, this governs the live API client. The default is
	// deliberately generous (3m) because fetchChapterPages forces Suwayomi to
	// contact the upstream source, which is legitimately slow — under
	// per-provider concurrency a shorter deadline caused frequent
	// "context deadline exceeded (Client.Timeout exceeded while awaiting
	// headers)" failures. Default 3m. validate() rejects a non-positive value.
	// Set via TSUNDOKU_SUWAYOMI_HTTPTIMEOUT.
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
	// Set via TSUNDOKU_SUWAYOMI_SEARCHTIMEOUT.
	SearchTimeout time.Duration
	// JavaPath is the path to the java executable used to launch the
	// Suwayomi JAR. Defaults to "java" (system PATH). Override when the
	// system default java is too old (Suwayomi v2.2.2100 requires Java 21+).
	// Set via TSUNDOKU_SUWAYOMI_JAVAPATH.
	// Example: /usr/lib/jvm/java-26-openjdk/bin/java
	JavaPath string
	// DatabaseType selects the embedded Suwayomi DB engine. Blank ⇒ Suwayomi's
	// default H2 (unchanged behaviour). "POSTGRESQL" ⇒ launch() passes the
	// Postgres -D system properties so the embedded JVM uses Postgres, whose
	// transactional DDL makes a killed migration roll back cleanly (vs H2's
	// auto-commit DDL → corruption-on-kill). EMBEDDED MODE ONLY — inert when
	// ExternalURL is set (external mode never launches a JVM). Recognised
	// tokens (confirmed against Suwayomi v2.2.2100 server-reference.conf):
	// H2, POSTGRESQL. Set via TSUNDOKU_SUWAYOMI_DATABASETYPE.
	DatabaseType string
	// DatabaseURL is the Postgres connection URL for the embedded Suwayomi
	// backend. Required when DatabaseType is POSTGRESQL; ignored for H2.
	//
	// IMPORTANT: this is the BARE postgresql:// form (e.g.
	// postgresql://host:5432/suwayomi) with NO "jdbc:" prefix — Suwayomi
	// prepends "jdbc:" itself (server-reference.conf default is
	// "postgresql://localhost:5432/suwayomi"; DBManager.createHikariDataSource
	// concatenates "jdbc:" + databaseUrl). Supplying a jdbc:postgresql:// value
	// would double the prefix and break boot, so validate() rejects it.
	// Set via TSUNDOKU_SUWAYOMI_DATABASEURL.
	DatabaseURL string
	// DatabaseUsername / DatabasePassword are the Postgres credentials for the
	// embedded Suwayomi backend (separate fields, not embedded in DatabaseURL).
	// Set via TSUNDOKU_SUWAYOMI_DATABASEUSERNAME / _DATABASEPASSWORD.
	DatabaseUsername string
	DatabasePassword string
}

// BaseURL returns the base HTTP URL for the Suwayomi server — the single
// resolution point for both lifecycle modes. In EXTERNAL mode (ExternalURL set)
// it returns ExternalURL with any trailing slash trimmed, so callers get a clean
// baseURL + path join. In EMBEDDED mode it returns the local http://Host:Port.
// BasePath is not included; callers append the path they need.
func (s SuwayomiConfig) BaseURL() string {
	if s.ExternalURL != "" {
		return strings.TrimRight(s.ExternalURL, "/")
	}
	return "http://" + s.Host + ":" + s.Port
}

// IsExternal reports whether tsundoku should target an external Suwayomi
// (ExternalURL set) rather than provisioning and running its own embedded JAR.
// main.go branches on this to skip the ProcessManager in external mode.
func (s SuwayomiConfig) IsExternal() bool { return s.ExternalURL != "" }

// JobsConfig holds background-job scheduler settings.
type JobsConfig struct {
	// DownloadInterval is the tick period for the download runner (queue drain
	// + upgrade-swap). Default 15m. Set via TSUNDOKU_JOBS_DOWNLOADINTERVAL.
	DownloadInterval time.Duration

	// DownloadConcurrency bounds how many chapter downloads run in parallel PER
	// PROVIDER in the dispatcher (each is a live upstream fetch). Default 4 —
	// unchanged from the previous hardcoded literal. validate() rejects a value
	// below 1. Set via TSUNDOKU_JOBS_DOWNLOADCONCURRENCY.
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

	// MaxRetries is how many times a failed chapter download is retried before it
	// is parked in permanently_failed. Default 3. This is the env-sourced DEFAULT
	// the runtime settings overlay (internal/settings) can override per-deployment
	// without a restart. Set via TSUNDOKU_JOBS_MAXRETRIES.
	MaxRetries int

	// RetryBackoff is the BASE delay before the first retry of a failed chapter;
	// the dispatcher doubles it per attempt (capped at 1h). Default 1m. Like
	// MaxRetries it is the env default behind the runtime settings overlay. Set
	// via TSUNDOKU_JOBS_RETRYBACKOFF.
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
}

// HealthConfig tunes the M7 source-health computation.
type HealthConfig struct {
	// StaleGraceDays is how old a source's newest chapter must be — on top of
	// the source having fallen behind the series' leading edge — before it is
	// reported "stale". Default 14. Set via TSUNDOKU_HEALTH_STALEGRACEDAYS.
	StaleGraceDays int
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
		// Suwayomi — M0 fields preserved; M2 fields added below.
		// externalurl blank ⇒ embedded mode (provision + run own JAR).
		"suwayomi.externalurl":         "",
		"suwayomi.host":                "localhost",
		"suwayomi.port":                "4567",
		"suwayomi.basepath":            "/api",
		"suwayomi.version":             suwayomiDefaultVersion,
		"suwayomi.runtimedir":          "/data/suwayomi",
		"suwayomi.downloadurltemplate": suwayomiDownloadURLTemplate,
		"suwayomi.starttimeout":        "2m",
		"suwayomi.downloadtimeout":     "10m",
		"suwayomi.httptimeout":         "3m",
		"suwayomi.searchtimeout":       "85s",
		"suwayomi.javapath":            "java",
		// Embedded-Suwayomi DB engine — all blank ⇒ disabled (Suwayomi's
		// default H2, unchanged behaviour). Set DatabaseType=POSTGRESQL to
		// opt the embedded JVM onto Postgres (kill-safe transactional DDL).
		"suwayomi.databasetype":     "",
		"suwayomi.databaseurl":      "",
		"suwayomi.databaseusername": "",
		"suwayomi.databasepassword": "",
		// Jobs — background-job scheduler.
		"jobs.downloadinterval":       "15m",
		"jobs.downloadconcurrency":    4,
		"jobs.refreshinterval":        "2h",
		"jobs.refreshconcurrency":     4,
		"jobs.maxretries":             3,
		"jobs.retrybackoff":           "1m",
		"jobs.extensioncheckinterval": "24h",
		"jobs.warmupinterval":         "15m",
		"jobs.warmupslowthresholdms":  5000,
		// Health — M7 source-health computation.
		"health.stalegracedays": 14,
		"storage.folder":        "/data/manga",
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
//	TSUNDOKU_SUWAYOMI_EXTERNALURL           → suwayomi.externalurl
//	TSUNDOKU_SUWAYOMI_HOST                  → suwayomi.host
//	TSUNDOKU_SUWAYOMI_BASEPATH              → suwayomi.basepath
//	TSUNDOKU_SUWAYOMI_VERSION               → suwayomi.version
//	TSUNDOKU_SUWAYOMI_RUNTIMEDIR            → suwayomi.runtimedir
//	TSUNDOKU_SUWAYOMI_DOWNLOADURLTEMPLATE   → suwayomi.downloadurltemplate
//	TSUNDOKU_SUWAYOMI_STARTTIMEOUT          → suwayomi.starttimeout
//	TSUNDOKU_SUWAYOMI_DOWNLOADTIMEOUT       → suwayomi.downloadtimeout
//	TSUNDOKU_SUWAYOMI_HTTPTIMEOUT           → suwayomi.httptimeout
//	TSUNDOKU_SUWAYOMI_SEARCHTIMEOUT         → suwayomi.searchtimeout
//	TSUNDOKU_SUWAYOMI_JAVAPATH              → suwayomi.javapath
//	TSUNDOKU_SUWAYOMI_DATABASETYPE          → suwayomi.databasetype
//	TSUNDOKU_SUWAYOMI_DATABASEURL           → suwayomi.databaseurl
//	TSUNDOKU_SUWAYOMI_DATABASEUSERNAME      → suwayomi.databaseusername
//	TSUNDOKU_SUWAYOMI_DATABASEPASSWORD      → suwayomi.databasepassword
//	TSUNDOKU_JOBS_DOWNLOADINTERVAL          → jobs.downloadinterval
//	TSUNDOKU_JOBS_DOWNLOADCONCURRENCY       → jobs.downloadconcurrency
//	TSUNDOKU_JOBS_REFRESHINTERVAL           → jobs.refreshinterval
//	TSUNDOKU_JOBS_REFRESHCONCURRENCY        → jobs.refreshconcurrency
//	TSUNDOKU_JOBS_MAXRETRIES                → jobs.maxretries
//	TSUNDOKU_JOBS_RETRYBACKOFF              → jobs.retrybackoff
//	TSUNDOKU_JOBS_EXTENSIONCHECKINTERVAL    → jobs.extensioncheckinterval
//	TSUNDOKU_JOBS_WARMUPINTERVAL            → jobs.warmupinterval
//	TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS     → jobs.warmupslowthresholdms
//	TSUNDOKU_STORAGE_FOLDER                 → storage.folder
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
//   - Suwayomi.ExternalURL, when set (external mode), must be a well-formed
//     absolute http/https URL — a malformed target aborts startup rather than
//     silently retargeting the client at an unreachable address.
//   - Suwayomi.DatabaseType, when set, must be H2 or POSTGRESQL; POSTGRESQL
//     additionally requires a valid postgresql:// Suwayomi.DatabaseURL (blank
//     ⇒ default H2, the unchanged behaviour).
//   - Suwayomi.HTTPTimeout must be positive — a zero/negative client deadline
//     would either never time out or reject every request.
//   - Jobs.DownloadConcurrency must be at least 1 — a non-positive per-provider
//     concurrency would stall the dispatcher entirely.
//   - Jobs.WarmupSlowThresholdMs must be at least 1 — a non-positive slow
//     threshold would flag every source slow on every warm pass.
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

	if err := validateExternalURL(c.Suwayomi.ExternalURL); err != nil {
		errs = append(errs, err.Error())
	}

	if err := validateSuwayomiDatabase(c.Suwayomi); err != nil {
		errs = append(errs, err.Error())
	}

	if c.Suwayomi.HTTPTimeout <= 0 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_SUWAYOMI_HTTPTIMEOUT must be positive (got %s)", c.Suwayomi.HTTPTimeout,
		))
	}

	if c.Suwayomi.SearchTimeout <= 0 {
		errs = append(errs, fmt.Sprintf(
			"TSUNDOKU_SUWAYOMI_SEARCHTIMEOUT must be positive (got %s)", c.Suwayomi.SearchTimeout,
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

	if len(errs) > 0 {
		return errors.New("config: invalid configuration: " + strings.Join(errs, "; "))
	}
	return nil
}

// validateExternalURL fails closed on a malformed Suwayomi external target.
// A blank value selects embedded mode and passes (the check is skipped). A
// non-blank value must parse as an absolute URL with an http/https scheme and a
// non-empty host; otherwise an error naming the bad value is returned so the
// operator sees exactly what was rejected.
func validateExternalURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("TSUNDOKU_SUWAYOMI_EXTERNALURL %q is not a valid URL: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf(
			"TSUNDOKU_SUWAYOMI_EXTERNALURL %q must be an absolute http/https URL", raw,
		)
	}
	return nil
}

// databaseTypeH2 and databaseTypePostgres are the two embedded-Suwayomi DB
// engine tokens. Confirmed against the Suwayomi v2.2.2100 server-reference.conf
// (server.databaseType = H2 # options: H2, POSTGRESQL).
const (
	databaseTypeH2       = "H2"
	databaseTypePostgres = "POSTGRESQL"
)

// validateSuwayomiDatabase fails closed on a misconfigured embedded-Suwayomi DB
// selection. A blank DatabaseType selects Suwayomi's default H2 and passes (the
// check is skipped, so existing deploys are byte-for-byte unchanged). A non-blank
// type must be one of the recognised tokens; POSTGRESQL additionally requires a
// valid postgresql:// DatabaseURL. The fields are inert in external mode, but the
// check runs unconditionally — it is cheap and a misconfiguration is worth
// surfacing regardless of mode.
func validateSuwayomiDatabase(s SuwayomiConfig) error {
	switch s.DatabaseType {
	case "":
		return nil // blank ⇒ default H2 (unchanged behaviour).
	case databaseTypeH2:
		return nil // explicit H2 needs no URL.
	case databaseTypePostgres:
		return validatePostgresURL(s.DatabaseURL)
	default:
		return fmt.Errorf(
			"TSUNDOKU_SUWAYOMI_DATABASETYPE %q is not recognised (want %s or %s)",
			s.DatabaseType, databaseTypeH2, databaseTypePostgres,
		)
	}
}

// validatePostgresURL fails closed unless raw is a non-blank absolute
// postgresql:// URL with a host.
//
// The value is the BARE Suwayomi databaseUrl form — NO "jdbc:" prefix: Suwayomi
// v2.2.2100 prepends "jdbc:" itself (DBManager.createHikariDataSource does
// "jdbc:" + databaseUrl; server-reference.conf default is
// "postgresql://localhost:5432/suwayomi"). A jdbc:postgresql:// value would
// double the prefix and break boot, so it is rejected here with a message
// explaining the expected shape.
func validatePostgresURL(raw string) error {
	if raw == "" {
		return errors.New(
			"TSUNDOKU_SUWAYOMI_DATABASEURL must be set when " +
				"TSUNDOKU_SUWAYOMI_DATABASETYPE=POSTGRESQL",
		)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("TSUNDOKU_SUWAYOMI_DATABASEURL %q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "postgresql" || u.Host == "" {
		return fmt.Errorf(
			"TSUNDOKU_SUWAYOMI_DATABASEURL %q must be an absolute postgresql:// URL "+
				"with a host (no jdbc: prefix — Suwayomi adds it)", raw,
		)
	}
	return nil
}
