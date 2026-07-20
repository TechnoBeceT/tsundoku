// Package config_test exercises the config package from the outside
// (black-box, per fleet standard §13).
package config_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
)

// TestDSN verifies that DatabaseConfig.DSN() formats the connection string
// exactly as the postgres driver expects.
func TestDSN(t *testing.T) {
	d := config.DatabaseConfig{
		Host:     "h",
		Port:     "5432",
		User:     "u",
		Password: "p",
		Name:     "db",
		SSLMode:  "disable",
	}
	got := d.DSN()
	want := "postgres://u:p@h:5432/db?sslmode=disable" //nolint:gosec // test fixture, not real credentials
	if got != want {
		t.Fatalf("DSN = %q want %q", got, want)
	}
}

// TestDSNEncodesSpecialChars verifies that a password containing URL-special
// characters (@ / # %) is percent-encoded so that the result is a valid URI
// that round-trips correctly through url.Parse.
func TestDSNEncodesSpecialChars(t *testing.T) {
	d := config.DatabaseConfig{
		Host:     "h",
		Port:     "5432",
		User:     "u",
		Password: "p@ss/w#rd",
		Name:     "db",
		SSLMode:  "disable",
	}
	dsn := d.DSN()

	// The raw special chars must NOT appear unencoded in the userinfo part.
	if strings.Contains(dsn, ":p@ss/w#rd@") {
		t.Fatalf("DSN contains unencoded special chars: %q", dsn)
	}

	// The DSN must be a valid URL.
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", dsn, err)
	}

	// Round-trip: the parsed password must equal the original.
	gotPw, ok := parsed.User.Password()
	if !ok {
		t.Fatalf("url.Parse result has no password set")
	}
	if gotPw != d.Password {
		t.Fatalf("round-trip password = %q, want %q", gotPw, d.Password)
	}
}

// TestLoadDefaults confirms that Load() applies sane defaults for all
// non-secret fields and that validate() passes when the required secrets are
// provided via the environment.
func TestLoadDefaults(t *testing.T) {
	// Required secrets — everything else should default.
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234") // >= 16 chars

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port == "" {
		t.Fatal("defaults not applied: Server.Port is empty")
	}
	if cfg.Database.Host == "" {
		t.Fatal("defaults not applied: Database.Host is empty")
	}
	if cfg.Engine.URL == "" {
		t.Fatal("defaults not applied: Engine.URL is empty")
	}
	if cfg.Storage.Folder == "" {
		t.Fatal("defaults not applied: Storage.Folder is empty")
	}
}

// TestLoadAppliesEnvOverride confirms that env vars override built-in defaults.
func TestLoadAppliesEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "secret")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234") // >= 16 chars
	t.Setenv("TSUNDOKU_SERVER_PORT", "9999")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port != "9999" {
		t.Fatalf("env override not applied: Server.Port = %q, want %q", cfg.Server.Port, "9999")
	}
}

// TestLoadEnvDatabaseFields confirms that all DatabaseConfig fields are
// settable via environment variables.
func TestLoadEnvDatabaseFields(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_HOST", "dbhost")
	t.Setenv("TSUNDOKU_DATABASE_PORT", "5433")
	t.Setenv("TSUNDOKU_DATABASE_USER", "myuser")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "mysecret")
	t.Setenv("TSUNDOKU_DATABASE_NAME", "mydb")
	t.Setenv("TSUNDOKU_DATABASE_SSLMODE", "require")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234") // >= 16 chars

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	d := cfg.Database
	if d.Host != "dbhost" {
		t.Errorf("Database.Host = %q, want %q", d.Host, "dbhost")
	}
	if d.Port != "5433" {
		t.Errorf("Database.Port = %q, want %q", d.Port, "5433")
	}
	if d.User != "myuser" {
		t.Errorf("Database.User = %q, want %q", d.User, "myuser")
	}
	if d.Password != "mysecret" {
		t.Errorf("Database.Password = %q, want %q", d.Password, "mysecret")
	}
	if d.Name != "mydb" {
		t.Errorf("Database.Name = %q, want %q", d.Name, "mydb")
	}
	if d.SSLMode != "require" {
		t.Errorf("Database.SSLMode = %q, want %q", d.SSLMode, "require")
	}
}

// TestValidateFailsClosed verifies that validate() refuses a Config that
// has no DB password set — fail-closed semantics, no silent defaults.
func TestValidateFailsClosed(t *testing.T) {
	cfg := &config.Config{} // zero value — no password
	if err := config.ExportValidateForTest(cfg); err == nil {
		t.Fatal("expected validate error for missing DB password, got nil")
	}
}

// TestDSNUsedByLoad confirms that Load() + DSN() work end-to-end: the
// DSN produced from a loaded config is well-formed.
func TestDSNUsedByLoad(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "loadpw")
	t.Setenv("TSUNDOKU_DATABASE_USER", "luser")
	t.Setenv("TSUNDOKU_DATABASE_HOST", "lhost")
	t.Setenv("TSUNDOKU_DATABASE_PORT", "5432")
	t.Setenv("TSUNDOKU_DATABASE_NAME", "ldb")
	t.Setenv("TSUNDOKU_DATABASE_SSLMODE", "disable")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234") // >= 16 chars

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dsn := cfg.Database.DSN()
	want := "postgres://luser:loadpw@lhost:5432/ldb?sslmode=disable" //nolint:gosec // test fixture, not real credentials
	if dsn != want {
		t.Fatalf("DSN from loaded config = %q, want %q", dsn, want)
	}
}

// TestLoadEnvStorageFolder confirms that the StorageConfig.Folder field is
// settable via environment variable.
func TestLoadEnvStorageFolder(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")                 // required to pass validate()
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234") // required to pass validate()
	t.Setenv("TSUNDOKU_STORAGE_FOLDER", "/mnt/manga")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Storage.Folder != "/mnt/manga" {
		t.Errorf("Storage.Folder = %q, want %q", cfg.Storage.Folder, "/mnt/manga")
	}
}

// TestValidateRejectsEmptyAuthSecret confirms that validate() refuses a config
// with no auth secret — an empty HMAC secret makes all tokens forgeable.
func TestValidateRejectsEmptyAuthSecret(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: ""},
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for empty auth secret, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_AUTH_SECRET") {
		t.Errorf("error should mention TSUNDOKU_AUTH_SECRET, got: %v", err)
	}
}

// TestValidateRejectsShortAuthSecret confirms that validate() refuses a secret
// shorter than the minimum (16 characters).
func TestValidateRejectsShortAuthSecret(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "tooshort"},
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for short auth secret, got nil")
	}
}

// TestValidateAcceptsValidAuthSecret confirms that a 16+ character secret
// passes validate() when combined with a valid DB password.
func TestValidateAcceptsValidAuthSecret(t *testing.T) {
	cfg := &config.Config{
		Database:   config.DatabaseConfig{Password: "somepassword"},
		Auth:       config.AuthConfig{Secret: "exactly16charssss"},
		Engine:     validEngineConfig(),
		Extensions: validExtensionsConfig(),
		Jobs:       config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
		Sources:    validSourcesConfig(),
	}
	if err := config.ExportValidateForTest(cfg); err != nil {
		t.Fatalf("expected validate to pass, got: %v", err)
	}
}

// validSourcesConfig returns a SourcesConfig that passes validate() — the
// fixture every "happy path" validate() test that doesn't itself exercise the
// Sources rules threads through, so adding those rules doesn't need to touch
// every unrelated fixture's Jobs/Engine/Auth values.
func validSourcesConfig() config.SourcesConfig {
	return config.SourcesConfig{FailureThreshold: 5, Cooldown: 30 * time.Minute, MinRequestDelay: 500 * time.Millisecond}
}

// validEngineConfig returns an EngineConfig that passes validate() — the
// fixture every "happy path" validate() test that doesn't itself exercise the
// Engine timeout rules threads through (mirrors validSourcesConfig).
func validEngineConfig() config.EngineConfig {
	return config.EngineConfig{HTTPTimeout: 3 * time.Minute, SearchTimeout: 3 * time.Minute}
}

// validExtensionsConfig returns an ExtensionsConfig that passes validate() — the
// fixture every "happy path" validate() test threads through so adding the
// retained-versions bound doesn't need to touch unrelated fixtures.
func validExtensionsConfig() config.ExtensionsConfig {
	return config.ExtensionsConfig{RetainedVersions: 3}
}

// TestLoadAuthSecretFromEnv confirms that TSUNDOKU_AUTH_SECRET is loaded and
// stored in Config.Auth.Secret.
func TestLoadAuthSecretFromEnv(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "mysupersecretauth123") // >= 16 chars

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Auth.Secret != "mysupersecretauth123" {
		t.Errorf("Auth.Secret = %q, want %q", cfg.Auth.Secret, "mysupersecretauth123")
	}
}

// TestLoadRejectsWithoutAuthSecret confirms that Load() fails closed when
// TSUNDOKU_AUTH_SECRET is absent, preventing startup with forgeable tokens.
func TestLoadRejectsWithoutAuthSecret(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	// Deliberately do not set TSUNDOKU_AUTH_SECRET

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected Load() to fail without auth secret, got nil")
	}
}

// TestJobsConfig confirms that JobsConfig fields are read from env vars
// and that a sane default is applied when the var is absent.
func TestJobsConfig(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_JOBS_DOWNLOADINTERVAL", "5m")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.DownloadInterval != 5*time.Minute {
		t.Errorf("Jobs.DownloadInterval = %v, want %v", cfg.Jobs.DownloadInterval, 5*time.Minute)
	}
}

// TestJobsDefaultInterval confirms that a sensible default is applied for
// Jobs.DownloadInterval when TSUNDOKU_JOBS_DOWNLOADINTERVAL is not set.
func TestJobsDefaultInterval(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.DownloadInterval <= 0 {
		t.Error("Jobs.DownloadInterval default must be positive")
	}
}

// TestJobsRefreshConfig confirms the M5 refresh fields are read from env vars.
func TestJobsRefreshConfig(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_JOBS_REFRESHINTERVAL", "30m")
	t.Setenv("TSUNDOKU_JOBS_REFRESHCONCURRENCY", "8")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jobs.RefreshInterval != 30*time.Minute {
		t.Errorf("Jobs.RefreshInterval = %v, want %v", cfg.Jobs.RefreshInterval, 30*time.Minute)
	}
	if cfg.Jobs.RefreshConcurrency != 8 {
		t.Errorf("Jobs.RefreshConcurrency = %d, want 8", cfg.Jobs.RefreshConcurrency)
	}
}

// TestJobsRefreshDefaults confirms sensible defaults when the env vars are unset.
func TestJobsRefreshDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jobs.RefreshInterval != 2*time.Hour {
		t.Errorf("Jobs.RefreshInterval default = %v, want 2h", cfg.Jobs.RefreshInterval)
	}
	if cfg.Jobs.RefreshConcurrency != 4 {
		t.Errorf("Jobs.RefreshConcurrency default = %d, want 4", cfg.Jobs.RefreshConcurrency)
	}
}

// TestJobsRetryConfig confirms the retry-policy fields are read from env vars.
func TestJobsRetryConfig(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_JOBS_MAXRETRIES", "7")
	t.Setenv("TSUNDOKU_JOBS_RETRYBACKOFF", "30s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jobs.MaxRetries != 7 {
		t.Errorf("Jobs.MaxRetries = %d, want 7", cfg.Jobs.MaxRetries)
	}
	if cfg.Jobs.RetryBackoff != 30*time.Second {
		t.Errorf("Jobs.RetryBackoff = %v, want 30s", cfg.Jobs.RetryBackoff)
	}
}

// TestJobsRetryDefaults confirms the Kaizoku-style defaults (5 retries, flat 30m
// backoff) when the retry env vars are unset.
func TestJobsRetryDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jobs.MaxRetries != 5 {
		t.Errorf("Jobs.MaxRetries default = %d, want 5", cfg.Jobs.MaxRetries)
	}
	if cfg.Jobs.RetryBackoff != 30*time.Minute {
		t.Errorf("Jobs.RetryBackoff default = %v, want 30m", cfg.Jobs.RetryBackoff)
	}
}

// TestEngineHTTPTimeoutDefault confirms that Load() applies the 3m default for
// the engine-host API-client timeout (raised from the old hardcoded 60s so
// that the slow page-image fetch does not time out under concurrency).
func TestEngineHTTPTimeoutDefault(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.HTTPTimeout != 3*time.Minute {
		t.Errorf("Engine.HTTPTimeout default = %v, want 3m", cfg.Engine.HTTPTimeout)
	}
}

// TestEngineHTTPTimeoutEnvOverride confirms TSUNDOKU_ENGINE_HTTPTIMEOUT
// overrides the default and unmarshals as a duration.
func TestEngineHTTPTimeoutEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_ENGINE_HTTPTIMEOUT", "90s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.HTTPTimeout != 90*time.Second {
		t.Errorf("Engine.HTTPTimeout = %v, want 90s", cfg.Engine.HTTPTimeout)
	}
}

// TestValidateRejectsNonPositiveHTTPTimeout confirms validate() fails closed on a
// zero/negative engine-host API-client timeout, naming the offending env var.
func TestValidateRejectsNonPositiveHTTPTimeout(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Engine:   config.EngineConfig{HTTPTimeout: 0, SearchTimeout: 3 * time.Minute}, // invalid
		Jobs:     config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for non-positive HTTPTimeout, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_ENGINE_HTTPTIMEOUT") {
		t.Errorf("error should name TSUNDOKU_ENGINE_HTTPTIMEOUT, got: %v", err)
	}
}

// TestEngineSearchTimeoutDefault confirms Load() applies the 85s default for
// the interactive search fan-out deadline — chosen to sit under a CDN edge's
// ~100s cut-off so a hung Cloudflare source yields partial results, not a 524.
func TestEngineSearchTimeoutDefault(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.SearchTimeout != 85*time.Second {
		t.Errorf("Engine.SearchTimeout default = %v, want 85s", cfg.Engine.SearchTimeout)
	}
}

// TestEngineSearchTimeoutEnvOverride confirms TSUNDOKU_ENGINE_SEARCHTIMEOUT
// overrides the default and unmarshals as a duration.
func TestEngineSearchTimeoutEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_ENGINE_SEARCHTIMEOUT", "45s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.SearchTimeout != 45*time.Second {
		t.Errorf("Engine.SearchTimeout = %v, want 45s", cfg.Engine.SearchTimeout)
	}
}

// TestValidateRejectsNonPositiveSearchTimeout confirms validate() fails closed on
// a zero/negative interactive-search deadline, naming the offending env var.
func TestValidateRejectsNonPositiveSearchTimeout(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Engine:   config.EngineConfig{HTTPTimeout: time.Minute, SearchTimeout: 0}, // invalid
		Jobs:     config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for non-positive SearchTimeout, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_ENGINE_SEARCHTIMEOUT") {
		t.Errorf("error should name TSUNDOKU_ENGINE_SEARCHTIMEOUT, got: %v", err)
	}
}

// TestEngineRuntimeDirDefault confirms Load() applies a non-empty default for
// the engine runtime dir (the extension-.apk byte cache root) when
// TSUNDOKU_ENGINE_RUNTIMEDIR is unset — the exact path is not pinned here (it
// carries forward the pre-existing /data/suwayomi default so an upgraded
// deploy's volume mount keeps working; see EngineConfig.RuntimeDir's doc
// comment) but it must never be blank.
func TestEngineRuntimeDirDefault(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.RuntimeDir == "" {
		t.Error("Engine.RuntimeDir default must not be empty")
	}
}

// TestEngineRuntimeDirEnvOverride confirms TSUNDOKU_ENGINE_RUNTIMEDIR
// overrides the default.
func TestEngineRuntimeDirEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_ENGINE_RUNTIMEDIR", "/tmp/engine-test")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.RuntimeDir != "/tmp/engine-test" {
		t.Errorf("Engine.RuntimeDir = %q, want %q", cfg.Engine.RuntimeDir, "/tmp/engine-test")
	}
}

// TestEngineHostLauncherDefaults confirms the per-profile process-launcher keys
// (HostBin/DataDir/KCEFBundle) apply their non-empty defaults when unset. The
// exact paths mirror the container image layout (see the Dockerfile).
func TestEngineHostLauncherDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.HostBin != "/app/engine-host/bin/tsundoku-engine-host" {
		t.Errorf("Engine.HostBin default = %q", cfg.Engine.HostBin)
	}
	if cfg.Engine.DataDir != "/config/engine" {
		t.Errorf("Engine.DataDir default = %q, want /config/engine", cfg.Engine.DataDir)
	}
	if cfg.Engine.KCEFBundle != "/app/kcef-runtime/bin/kcef" {
		t.Errorf("Engine.KCEFBundle default = %q", cfg.Engine.KCEFBundle)
	}
}

// TestEngineHostLauncherEnvOverride confirms each launcher key is overridable —
// in particular that DataDir binds to TSUNDOKU_ENGINE_DATA (the SAME env var the
// entrypoint reads), via the `koanf:"data"` tag.
func TestEngineHostLauncherEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_ENGINE_HOSTBIN", "/opt/host")
	t.Setenv("TSUNDOKU_ENGINE_DATA", "/mnt/engine")
	t.Setenv("TSUNDOKU_ENGINE_KCEFBUNDLE", "/opt/kcef")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.HostBin != "/opt/host" {
		t.Errorf("Engine.HostBin = %q, want /opt/host", cfg.Engine.HostBin)
	}
	if cfg.Engine.DataDir != "/mnt/engine" {
		t.Errorf("Engine.DataDir = %q, want /mnt/engine (TSUNDOKU_ENGINE_DATA)", cfg.Engine.DataDir)
	}
	if cfg.Engine.KCEFBundle != "/opt/kcef" {
		t.Errorf("Engine.KCEFBundle = %q, want /opt/kcef", cfg.Engine.KCEFBundle)
	}
}

// TestEngineHostLauncherKeysNotFailClosed confirms the three launcher paths are
// NOT validated at startup — a deploy with no network bindings must boot even if
// they point at nonexistent paths (they are only dereferenced on an actual
// profile spawn, which degrades to the default instance on failure).
func TestEngineHostLauncherKeysNotFailClosed(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_ENGINE_HOSTBIN", "/no/such/binary")
	t.Setenv("TSUNDOKU_ENGINE_DATA", "/no/such/dir")
	t.Setenv("TSUNDOKU_ENGINE_KCEFBUNDLE", "/no/such/bundle")

	if _, err := config.Load(); err != nil {
		t.Fatalf("load must not fail closed on nonexistent launcher paths: %v", err)
	}
}

// TestJobsDownloadConcurrencyDefault confirms the per-source download
// concurrency defaults to 5 (Kaizoku parity).
func TestJobsDownloadConcurrencyDefault(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.DownloadConcurrency != 5 {
		t.Errorf("Jobs.DownloadConcurrency default = %d, want 5", cfg.Jobs.DownloadConcurrency)
	}
}

// TestJobsDownloadConcurrencyEnvOverride confirms TSUNDOKU_JOBS_DOWNLOADCONCURRENCY
// overrides the default.
func TestJobsDownloadConcurrencyEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_JOBS_DOWNLOADCONCURRENCY", "8")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.DownloadConcurrency != 8 {
		t.Errorf("Jobs.DownloadConcurrency = %d, want 8", cfg.Jobs.DownloadConcurrency)
	}
}

// TestValidateRejectsDownloadConcurrencyBelowOne confirms validate() fails closed
// when the per-provider download concurrency is below 1, naming the env var.
func TestValidateRejectsDownloadConcurrencyBelowOne(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Engine:   validEngineConfig(),
		Jobs:     config.JobsConfig{DownloadConcurrency: 0}, // invalid
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for DownloadConcurrency < 1, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_JOBS_DOWNLOADCONCURRENCY") {
		t.Errorf("error should name TSUNDOKU_JOBS_DOWNLOADCONCURRENCY, got: %v", err)
	}
}

// TestJobsWarmupDefaults confirms the warm-up interval defaults to 15m and the
// slow threshold to 5000ms when their env vars are unset.
func TestJobsWarmupDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.WarmupInterval != 15*time.Minute {
		t.Errorf("Jobs.WarmupInterval default = %v, want 15m", cfg.Jobs.WarmupInterval)
	}
	if cfg.Jobs.WarmupSlowThresholdMs != 5000 {
		t.Errorf("Jobs.WarmupSlowThresholdMs default = %d, want 5000", cfg.Jobs.WarmupSlowThresholdMs)
	}
}

// TestJobsWarmupEnvOverride confirms the warm-up env vars override the defaults.
func TestJobsWarmupEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_JOBS_WARMUPINTERVAL", "30m")
	t.Setenv("TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS", "8000")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.WarmupInterval != 30*time.Minute {
		t.Errorf("Jobs.WarmupInterval = %v, want 30m", cfg.Jobs.WarmupInterval)
	}
	if cfg.Jobs.WarmupSlowThresholdMs != 8000 {
		t.Errorf("Jobs.WarmupSlowThresholdMs = %d, want 8000", cfg.Jobs.WarmupSlowThresholdMs)
	}
}

// TestJobsTrackRetryIntervalDefault confirms the tracker-push retry-queue
// drain interval defaults to 5m when its env var is unset.
func TestJobsTrackRetryIntervalDefault(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.TrackRetryInterval != 5*time.Minute {
		t.Errorf("Jobs.TrackRetryInterval default = %v, want 5m", cfg.Jobs.TrackRetryInterval)
	}
}

// TestJobsTrackRetryIntervalEnvOverride confirms the env var overrides the
// tracker-push retry-queue drain interval default.
func TestJobsTrackRetryIntervalEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_JOBS_TRACKRETRYINTERVAL", "10m")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.TrackRetryInterval != 10*time.Minute {
		t.Errorf("Jobs.TrackRetryInterval = %v, want 10m", cfg.Jobs.TrackRetryInterval)
	}
}

// TestJobsCacheTTLDefaults confirms the interactive-cache TTL fields default to
// 1h when their env vars are unset.
func TestJobsCacheTTLDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.SearchCacheTTL != time.Hour {
		t.Errorf("Jobs.SearchCacheTTL default = %v, want 1h", cfg.Jobs.SearchCacheTTL)
	}
	if cfg.Jobs.ChapterCacheTTL != time.Hour {
		t.Errorf("Jobs.ChapterCacheTTL default = %v, want 1h", cfg.Jobs.ChapterCacheTTL)
	}
}

// TestJobsCacheTTLEnvOverride confirms the cache-TTL env vars override the defaults.
func TestJobsCacheTTLEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_JOBS_SEARCHCACHETTL", "30m")
	t.Setenv("TSUNDOKU_JOBS_CHAPTERCACHETTL", "2h")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Jobs.SearchCacheTTL != 30*time.Minute {
		t.Errorf("Jobs.SearchCacheTTL = %v, want 30m", cfg.Jobs.SearchCacheTTL)
	}
	if cfg.Jobs.ChapterCacheTTL != 2*time.Hour {
		t.Errorf("Jobs.ChapterCacheTTL = %v, want 2h", cfg.Jobs.ChapterCacheTTL)
	}
}

// TestValidateRejectsWarmupThresholdBelowOne confirms validate() fails closed when
// the slow threshold is below 1, naming the env var.
func TestValidateRejectsWarmupThresholdBelowOne(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Engine:   validEngineConfig(),
		Jobs:     config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 0}, // invalid
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for WarmupSlowThresholdMs < 1, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS") {
		t.Errorf("error should name TSUNDOKU_JOBS_WARMUPSLOWTHRESHOLDMS, got: %v", err)
	}
}

// TestSourcesDefaults confirms the source-politeness defaults (failure
// threshold 5, cooldown 30m, min request delay 500ms) are applied when their
// env vars are unset.
func TestSourcesDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Sources.FailureThreshold != 5 {
		t.Errorf("Sources.FailureThreshold default = %d, want 5", cfg.Sources.FailureThreshold)
	}
	if cfg.Sources.Cooldown != 30*time.Minute {
		t.Errorf("Sources.Cooldown default = %v, want 30m", cfg.Sources.Cooldown)
	}
	if cfg.Sources.MinRequestDelay != 500*time.Millisecond {
		t.Errorf("Sources.MinRequestDelay default = %v, want 500ms", cfg.Sources.MinRequestDelay)
	}
}

// TestSourcesEnvOverride confirms the TSUNDOKU_SOURCES_* env vars override the
// source-politeness defaults.
func TestSourcesEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_SOURCES_FAILURETHRESHOLD", "3")
	t.Setenv("TSUNDOKU_SOURCES_COOLDOWN", "10m")
	t.Setenv("TSUNDOKU_SOURCES_MINREQUESTDELAY", "1s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Sources.FailureThreshold != 3 {
		t.Errorf("Sources.FailureThreshold = %d, want 3", cfg.Sources.FailureThreshold)
	}
	if cfg.Sources.Cooldown != 10*time.Minute {
		t.Errorf("Sources.Cooldown = %v, want 10m", cfg.Sources.Cooldown)
	}
	if cfg.Sources.MinRequestDelay != time.Second {
		t.Errorf("Sources.MinRequestDelay = %v, want 1s", cfg.Sources.MinRequestDelay)
	}
}

// TestValidateRejectsSourcesFailureThresholdBelowOne confirms validate() fails
// closed when the breaker's trip threshold is below 1, naming the env var.
func TestValidateRejectsSourcesFailureThresholdBelowOne(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Engine:   validEngineConfig(),
		Jobs:     config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
		Sources:  config.SourcesConfig{FailureThreshold: 0, Cooldown: 30 * time.Minute}, // invalid
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for FailureThreshold < 1, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_SOURCES_FAILURETHRESHOLD") {
		t.Errorf("error should name TSUNDOKU_SOURCES_FAILURETHRESHOLD, got: %v", err)
	}
}

// TestValidateRejectsSourcesCooldownBelowOneMinute confirms validate() fails
// closed when the breaker cooldown is below 1 minute, naming the env var.
func TestValidateRejectsSourcesCooldownBelowOneMinute(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Engine:   validEngineConfig(),
		Jobs:     config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
		Sources:  config.SourcesConfig{FailureThreshold: 5, Cooldown: 30 * time.Second}, // invalid
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for Cooldown < 1m, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_SOURCES_COOLDOWN") {
		t.Errorf("error should name TSUNDOKU_SOURCES_COOLDOWN, got: %v", err)
	}
}

// TestValidateRejectsSourcesMinRequestDelayNegative confirms validate() fails
// closed on a negative politeness delay, naming the env var. 0 (disabled) must
// still be accepted.
func TestValidateRejectsSourcesMinRequestDelayNegative(t *testing.T) {
	cfg := &config.Config{
		Database:   config.DatabaseConfig{Password: "somepassword"},
		Auth:       config.AuthConfig{Secret: "exactly16charssss"},
		Engine:     validEngineConfig(),
		Extensions: validExtensionsConfig(),
		Jobs:       config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
		Sources:    config.SourcesConfig{FailureThreshold: 5, Cooldown: 30 * time.Minute, MinRequestDelay: -time.Second}, // invalid
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for negative MinRequestDelay, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_SOURCES_MINREQUESTDELAY") {
		t.Errorf("error should name TSUNDOKU_SOURCES_MINREQUESTDELAY, got: %v", err)
	}

	// 0 (disabled) must still be accepted.
	cfg.Sources.MinRequestDelay = 0
	if err := config.ExportValidateForTest(cfg); err != nil {
		t.Errorf("validate() rejected MinRequestDelay=0 (disabled), want accept: %v", err)
	}
}

// TestLoadDefaultsHealthStaleGrace confirms that Load() applies the default
// value (14) for Health.StaleGraceDays when TSUNDOKU_HEALTH_STALEGRACEDAYS
// is not set.
func TestLoadDefaultsHealthStaleGrace(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Health.StaleGraceDays != 14 {
		t.Fatalf("Health.StaleGraceDays default = %d, want 14", cfg.Health.StaleGraceDays)
	}
}

// TestLoadAppliesHealthEnvOverride confirms that TSUNDOKU_HEALTH_STALEGRACEDAYS
// overrides the built-in default for Health.StaleGraceDays.
func TestLoadAppliesHealthEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_HEALTH_STALEGRACEDAYS", "30")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Health.StaleGraceDays != 30 {
		t.Fatalf("Health.StaleGraceDays env override = %d, want 30", cfg.Health.StaleGraceDays)
	}
}

// TestAuthConfig_CookieSecureDefaultsTrue confirms that Load() applies the default
// value (true) for Auth.CookieSecure when TSUNDOKU_AUTH_COOKIESECURE is not set.
func TestAuthConfig_CookieSecureDefaultsTrue(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Auth.CookieSecure {
		t.Fatalf("CookieSecure: want true by default, got false")
	}
}

// TestAuthConfig_CookieSecureEnvOverride confirms that TSUNDOKU_AUTH_COOKIESECURE
// overrides the built-in default for Auth.CookieSecure.
func TestAuthConfig_CookieSecureEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")
	t.Setenv("TSUNDOKU_AUTH_COOKIESECURE", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.CookieSecure {
		t.Fatalf("CookieSecure: want false from env, got true")
	}
}

// TestMetadataConfig_DefaultsEmpty confirms that Metadata.MALClientID defaults
// to "" — MAL is optional (AniList + MangaDex carry the engine), so an
// unconfigured MAL client-id must never fail Load()/validate().
func TestMetadataConfig_DefaultsEmpty(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Metadata.MALClientID != "" {
		t.Fatalf("Metadata.MALClientID default = %q, want \"\"", cfg.Metadata.MALClientID)
	}
}

// TestMetadataConfig_MALClientIDFromEnv confirms TSUNDOKU_METADATA_MAL_CLIENTID
// populates Metadata.MALClientID — the koanf tag on that field (see its doc
// comment) is what makes the underscore-carrying env suffix resolve correctly.
func TestMetadataConfig_MALClientIDFromEnv(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")
	t.Setenv("TSUNDOKU_METADATA_MAL_CLIENTID", "abc123clientid")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Metadata.MALClientID != "abc123clientid" {
		t.Fatalf("Metadata.MALClientID = %q, want %q", cfg.Metadata.MALClientID, "abc123clientid")
	}
}

// TestTrackerConfig_DefaultsEmpty confirms every Tracker field defaults to
// "" — the whole Phase-3 OAuth subsystem is dormant/config-gated until the
// owner activates it (spec/trackers-oauth-phase3 §2), so a fresh install
// with no tracker env vars set must still Load() cleanly.
func TestTrackerConfig_DefaultsEmpty(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tracker.AniListClientID != "" || cfg.Tracker.MALClientID != "" || cfg.Tracker.MALClientSecret != "" || cfg.Tracker.PublicURL != "" {
		t.Fatalf("Tracker defaults = %+v, want all blank", cfg.Tracker)
	}
}

// TestTrackerConfig_FromEnv confirms all four TSUNDOKU_TRACKER_* env vars
// populate their respective fields.
func TestTrackerConfig_FromEnv(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")
	t.Setenv("TSUNDOKU_TRACKER_ANILISTCLIENTID", "anilist-cid")
	t.Setenv("TSUNDOKU_TRACKER_MALCLIENTID", "mal-cid")
	t.Setenv("TSUNDOKU_TRACKER_MALCLIENTSECRET", "mal-secret")
	t.Setenv("TSUNDOKU_TRACKER_PUBLICURL", "https://tsundoku.example")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tracker.AniListClientID != "anilist-cid" {
		t.Fatalf("Tracker.AniListClientID = %q, want anilist-cid", cfg.Tracker.AniListClientID)
	}
	if cfg.Tracker.MALClientID != "mal-cid" {
		t.Fatalf("Tracker.MALClientID = %q, want mal-cid", cfg.Tracker.MALClientID)
	}
	if cfg.Tracker.MALClientSecret != "mal-secret" {
		t.Fatalf("Tracker.MALClientSecret = %q, want mal-secret", cfg.Tracker.MALClientSecret)
	}
	if cfg.Tracker.PublicURL != "https://tsundoku.example" {
		t.Fatalf("Tracker.PublicURL = %q, want https://tsundoku.example", cfg.Tracker.PublicURL)
	}
}

// TestTrackerConfig_MalformedPublicURLFailsClosed confirms a non-blank but
// malformed Tracker.PublicURL aborts startup — a broken redirect_uri base
// would build an OAuth callback URL no provider could ever reach.
func TestTrackerConfig_MalformedPublicURLFailsClosed(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")
	t.Setenv("TSUNDOKU_TRACKER_PUBLICURL", "not-a-url")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load with a malformed TSUNDOKU_TRACKER_PUBLICURL: want an error, got nil")
	}
}

// TestTrackerConfig_BlankPublicURLPasses confirms a blank PublicURL is NOT
// a validation error — it just leaves the subsystem dormant.
func TestTrackerConfig_BlankPublicURLPasses(t *testing.T) {
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load with a blank TSUNDOKU_TRACKER_PUBLICURL: %v", err)
	}
}

// TestExtensionsConfig_DefaultAndEnvOverride confirms Load() defaults
// ExtensionsConfig.RetainedVersions to 3 and honours the env override.
func TestExtensionsConfig_DefaultAndEnvOverride(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Extensions.RetainedVersions != 3 {
		t.Fatalf("RetainedVersions default = %d, want 3", cfg.Extensions.RetainedVersions)
	}

	t.Setenv("TSUNDOKU_EXTENSIONS_RETAINEDVERSIONS", "8")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Extensions.RetainedVersions != 8 {
		t.Fatalf("RetainedVersions env override = %d, want 8", cfg.Extensions.RetainedVersions)
	}
}

// TestValidateRejectsRetainedVersionsOutOfBounds confirms validate() fails closed
// on a retained-versions value outside [1, 20], naming the env var.
func TestValidateRejectsRetainedVersionsOutOfBounds(t *testing.T) {
	cfg := &config.Config{
		Database:   config.DatabaseConfig{Password: "somepassword"},
		Auth:       config.AuthConfig{Secret: "exactly16charssss"},
		Engine:     validEngineConfig(),
		Extensions: config.ExtensionsConfig{RetainedVersions: 21}, // invalid
		Jobs:       config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
		Sources:    validSourcesConfig(),
	}
	err := config.ExportValidateForTest(cfg)
	if err == nil {
		t.Fatal("expected validate error for RetainedVersions=21, got nil")
	}
	if !strings.Contains(err.Error(), "TSUNDOKU_EXTENSIONS_RETAINEDVERSIONS") {
		t.Errorf("error should name TSUNDOKU_EXTENSIONS_RETAINEDVERSIONS, got: %v", err)
	}
}

// TestEngineConfig_DefaultURL confirms Load() defaults EngineConfig.URL to the
// engine-host's default listen address (http://localhost:7777 — confirmed
// against engine-host/src/main/kotlin/enginehost/Main.kt's
// TSUNDOKU_ENGINE_PORT fallback) when TSUNDOKU_ENGINE_URL is unset.
func TestEngineConfig_DefaultURL(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.URL != "http://localhost:7777" {
		t.Fatalf("Engine.URL = %q, want %q", cfg.Engine.URL, "http://localhost:7777")
	}
}

// TestEngineConfig_FromEnv confirms TSUNDOKU_ENGINE_URL overrides the default.
func TestEngineConfig_FromEnv(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_ENGINE_URL", "http://engine-host:9000")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.URL != "http://engine-host:9000" {
		t.Fatalf("Engine.URL = %q, want %q", cfg.Engine.URL, "http://engine-host:9000")
	}
}

// TestValidateAcceptsEngineURL confirms validate() accepts the default plus any
// well-formed absolute http(s) URL.
func TestValidateAcceptsEngineURL(t *testing.T) {
	for _, raw := range []string{"http://localhost:7777", "https://engine.example/rpc"} {
		cfg := &config.Config{
			Database:   config.DatabaseConfig{Password: "somepassword"},
			Auth:       config.AuthConfig{Secret: "exactly16charssss"},
			Jobs:       config.JobsConfig{DownloadConcurrency: 4, WarmupSlowThresholdMs: 5000},
			Sources:    validSourcesConfig(),
			Extensions: validExtensionsConfig(),
			Engine:     config.EngineConfig{URL: raw, HTTPTimeout: 3 * time.Minute, SearchTimeout: 3 * time.Minute},
		}
		if err := config.ExportValidateForTest(cfg); err != nil {
			t.Errorf("validate() rejected Engine.URL %q, want accept: %v", raw, err)
		}
	}
}

// TestValidateRejectsMalformedEngineURL confirms validate() fails closed on a
// non-empty, malformed Engine.URL, and that the error names the bad key.
func TestValidateRejectsMalformedEngineURL(t *testing.T) {
	for _, raw := range []string{"not-a-url", "ftp://x", "://x", "http://"} {
		cfg := &config.Config{
			Database: config.DatabaseConfig{Password: "somepassword"},
			Auth:     config.AuthConfig{Secret: "exactly16charssss"},
			Engine:   config.EngineConfig{URL: raw},
		}
		err := config.ExportValidateForTest(cfg)
		if err == nil {
			t.Errorf("validate() accepted malformed Engine.URL %q, want reject", raw)
			continue
		}
		if !strings.Contains(err.Error(), "TSUNDOKU_ENGINE_URL") {
			t.Errorf("error for %q should name TSUNDOKU_ENGINE_URL, got: %v", raw, err)
		}
	}
}
