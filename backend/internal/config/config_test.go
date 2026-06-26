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
	if cfg.Suwayomi.Host == "" {
		t.Fatal("defaults not applied: Suwayomi.Host is empty")
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

// TestLoadEnvSuwayomiFields confirms that all SuwayomiConfig fields are
// settable via environment variables.
func TestLoadEnvSuwayomiFields(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")                 // required to pass validate()
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234") // required to pass validate()
	t.Setenv("TSUNDOKU_SUWAYOMI_HOST", "suwhost")
	t.Setenv("TSUNDOKU_SUWAYOMI_PORT", "9999")
	t.Setenv("TSUNDOKU_SUWAYOMI_BASEPATH", "/graphql")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s := cfg.Suwayomi
	if s.Host != "suwhost" {
		t.Errorf("Suwayomi.Host = %q, want %q", s.Host, "suwhost")
	}
	if s.Port != "9999" {
		t.Errorf("Suwayomi.Port = %q, want %q", s.Port, "9999")
	}
	if s.BasePath != "/graphql" {
		t.Errorf("Suwayomi.BasePath = %q, want %q", s.BasePath, "/graphql")
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
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
	}
	if err := config.ExportValidateForTest(cfg); err != nil {
		t.Fatalf("expected validate to pass, got: %v", err)
	}
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

// TestLoadSuwayomiM2Fields confirms that the M2 SuwayomiConfig fields
// (Version, RuntimeDir, DownloadURLTemplate, StartTimeout, DownloadTimeout)
// are readable from environment variables and that typed durations unmarshal
// correctly.
func TestLoadSuwayomiM2Fields(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_SUWAYOMI_VERSION", "v9.9.9999")
	t.Setenv("TSUNDOKU_SUWAYOMI_RUNTIMEDIR", "/tmp/suwayomi-test")
	t.Setenv("TSUNDOKU_SUWAYOMI_DOWNLOADURLTEMPLATE", "https://example.com/%s/%s.jar")
	t.Setenv("TSUNDOKU_SUWAYOMI_STARTTIMEOUT", "3m")
	t.Setenv("TSUNDOKU_SUWAYOMI_DOWNLOADTIMEOUT", "10m")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s := cfg.Suwayomi
	if s.Version != "v9.9.9999" {
		t.Errorf("Suwayomi.Version = %q, want %q", s.Version, "v9.9.9999")
	}
	if s.RuntimeDir != "/tmp/suwayomi-test" {
		t.Errorf("Suwayomi.RuntimeDir = %q, want %q", s.RuntimeDir, "/tmp/suwayomi-test")
	}
	if s.DownloadURLTemplate != "https://example.com/%s/%s.jar" {
		t.Errorf("Suwayomi.DownloadURLTemplate = %q, want %q", s.DownloadURLTemplate, "https://example.com/%s/%s.jar")
	}
	if s.StartTimeout != 3*time.Minute {
		t.Errorf("Suwayomi.StartTimeout = %v, want %v", s.StartTimeout, 3*time.Minute)
	}
	if s.DownloadTimeout != 10*time.Minute {
		t.Errorf("Suwayomi.DownloadTimeout = %v, want %v", s.DownloadTimeout, 10*time.Minute)
	}
}

// TestSuwayomiDefaults confirms that Load() applies the pinned defaults for
// all new SuwayomiConfig fields when no env vars are set. The exact values are
// asserted so that accidental changes to pinned constants are caught.
func TestSuwayomiDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s := cfg.Suwayomi
	const wantVersion = "v2.2.2100"
	if s.Version != wantVersion {
		t.Errorf("Suwayomi.Version default = %q, want %q", s.Version, wantVersion)
	}
	const wantTemplate = "https://github.com/Suwayomi/Suwayomi-Server/releases/download/%s/Suwayomi-Server-%s.jar"
	if s.DownloadURLTemplate != wantTemplate {
		t.Errorf("Suwayomi.DownloadURLTemplate default = %q, want %q", s.DownloadURLTemplate, wantTemplate)
	}
	if s.RuntimeDir == "" {
		t.Error("Suwayomi.RuntimeDir default must not be empty")
	}
	if s.StartTimeout <= 0 {
		t.Error("Suwayomi.StartTimeout default must be positive")
	}
	if s.DownloadTimeout <= 0 {
		t.Error("Suwayomi.DownloadTimeout default must be positive")
	}
}

// TestSuwayomiBaseURL confirms that BaseURL() produces the correct
// http://host:port string from SuwayomiConfig.
func TestSuwayomiBaseURL(t *testing.T) {
	s := config.SuwayomiConfig{Host: "127.0.0.1", Port: "4567"}
	got := s.BaseURL()
	want := "http://127.0.0.1:4567"
	if got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}
}

// TestSuwayomiBaseURLEmbedded confirms BaseURL() returns the local
// http://host:port when ExternalURL is blank (embedded mode). Non-vacuous:
// setting ExternalURL would change the result (see TestSuwayomiBaseURLExternal).
func TestSuwayomiBaseURLEmbedded(t *testing.T) {
	s := config.SuwayomiConfig{Host: "127.0.0.1", Port: "4567"}
	got := s.BaseURL()
	want := "http://127.0.0.1:4567"
	if got != want {
		t.Errorf("BaseURL() embedded = %q, want %q", got, want)
	}
}

// TestSuwayomiBaseURLExternal confirms BaseURL() returns the external URL
// (overriding host:port) and trims a trailing slash for a clean path join.
func TestSuwayomiBaseURLExternal(t *testing.T) {
	s := config.SuwayomiConfig{
		Host:        "127.0.0.1",
		Port:        "4567",
		ExternalURL: "https://suwayomi.homelab.example/",
	}
	got := s.BaseURL()
	want := "https://suwayomi.homelab.example"
	if got != want {
		t.Errorf("BaseURL() external = %q, want %q", got, want)
	}
}

// TestSuwayomiIsExternal confirms IsExternal() reflects whether ExternalURL
// is set — the branch main.go uses to skip the embedded ProcessManager.
func TestSuwayomiIsExternal(t *testing.T) {
	if (config.SuwayomiConfig{}).IsExternal() {
		t.Error("IsExternal() = true for blank ExternalURL, want false")
	}
	if !(config.SuwayomiConfig{ExternalURL: "http://x:4567"}).IsExternal() {
		t.Error("IsExternal() = false for set ExternalURL, want true")
	}
}

// TestValidateAcceptsExternalURL confirms a well-formed http/https external URL
// passes validate() (and that blank — embedded mode — also passes).
func TestValidateAcceptsExternalURL(t *testing.T) {
	for _, raw := range []string{"", "http://localhost:4567", "https://suwayomi.example/api"} {
		cfg := &config.Config{
			Database: config.DatabaseConfig{Password: "somepassword"},
			Auth:     config.AuthConfig{Secret: "exactly16charssss"},
			Suwayomi: config.SuwayomiConfig{ExternalURL: raw},
		}
		if err := config.ExportValidateForTest(cfg); err != nil {
			t.Errorf("validate() rejected ExternalURL %q, want accept: %v", raw, err)
		}
	}
}

// TestValidateRejectsMalformedExternalURL confirms validate() fails closed on a
// malformed or scheme-less external URL, and that the error names the bad value.
func TestValidateRejectsMalformedExternalURL(t *testing.T) {
	for _, raw := range []string{"not a url", "ftp://x", "://x", "http://"} {
		cfg := &config.Config{
			Database: config.DatabaseConfig{Password: "somepassword"},
			Auth:     config.AuthConfig{Secret: "exactly16charssss"},
			Suwayomi: config.SuwayomiConfig{ExternalURL: raw},
		}
		err := config.ExportValidateForTest(cfg)
		if err == nil {
			t.Errorf("validate() accepted malformed ExternalURL %q, want reject", raw)
			continue
		}
		if !strings.Contains(err.Error(), "TSUNDOKU_SUWAYOMI_EXTERNALURL") {
			t.Errorf("error for %q should name TSUNDOKU_SUWAYOMI_EXTERNALURL, got: %v", raw, err)
		}
	}
}

// TestLoadEnvSuwayomiExternalURL confirms TSUNDOKU_SUWAYOMI_EXTERNALURL
// populates SuwayomiConfig.ExternalURL and flips the resolved mode.
func TestLoadEnvSuwayomiExternalURL(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_SUWAYOMI_EXTERNALURL", "http://homelab:4567")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Suwayomi.ExternalURL != "http://homelab:4567" {
		t.Errorf("Suwayomi.ExternalURL = %q, want %q", cfg.Suwayomi.ExternalURL, "http://homelab:4567")
	}
	if !cfg.Suwayomi.IsExternal() {
		t.Error("IsExternal() = false after setting TSUNDOKU_SUWAYOMI_EXTERNALURL, want true")
	}
	if cfg.Suwayomi.BaseURL() != "http://homelab:4567" {
		t.Errorf("BaseURL() = %q, want %q", cfg.Suwayomi.BaseURL(), "http://homelab:4567")
	}
}

// TestSuwayomiExternalURLDefaultBlank confirms the default is blank (embedded
// mode) so existing deploys are byte-for-byte unchanged.
func TestSuwayomiExternalURLDefaultBlank(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Suwayomi.ExternalURL != "" {
		t.Errorf("Suwayomi.ExternalURL default = %q, want empty (embedded)", cfg.Suwayomi.ExternalURL)
	}
	if cfg.Suwayomi.IsExternal() {
		t.Error("IsExternal() = true by default, want false (embedded)")
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

// TestSuwayomiJavaPathDefault confirms that JavaPath defaults to "java"
// when TSUNDOKU_SUWAYOMI_JAVAPATH is not set.
func TestSuwayomiJavaPathDefault(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Suwayomi.JavaPath != "java" {
		t.Errorf("Suwayomi.JavaPath default = %q, want %q", cfg.Suwayomi.JavaPath, "java")
	}
}

// TestSuwayomiJavaPathEnv confirms that TSUNDOKU_SUWAYOMI_JAVAPATH overrides
// the default java executable path.
func TestSuwayomiJavaPathEnv(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_SUWAYOMI_JAVAPATH", "/usr/lib/jvm/java-26-openjdk/bin/java")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := "/usr/lib/jvm/java-26-openjdk/bin/java"
	if cfg.Suwayomi.JavaPath != want {
		t.Errorf("Suwayomi.JavaPath = %q, want %q", cfg.Suwayomi.JavaPath, want)
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

// TestJobsRetryDefaults confirms sensible defaults (3 retries, 1m backoff) when
// the retry env vars are unset.
func TestJobsRetryDefaults(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "0123456789abcdef0123456789abcdef")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jobs.MaxRetries != 3 {
		t.Errorf("Jobs.MaxRetries default = %d, want 3", cfg.Jobs.MaxRetries)
	}
	if cfg.Jobs.RetryBackoff != time.Minute {
		t.Errorf("Jobs.RetryBackoff default = %v, want 1m", cfg.Jobs.RetryBackoff)
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

// TestSuwayomiDatabaseDefaultsBlank confirms the embedded-Suwayomi DB fields
// default to blank, preserving the current H2 behaviour (zero surprise for
// existing deploys).
func TestSuwayomiDatabaseDefaultsBlank(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s := cfg.Suwayomi
	if s.DatabaseType != "" || s.DatabaseURL != "" || s.DatabaseUsername != "" || s.DatabasePassword != "" {
		t.Errorf("embedded-Suwayomi DB fields should default blank, got type=%q url=%q user=%q passSet=%t",
			s.DatabaseType, s.DatabaseURL, s.DatabaseUsername, s.DatabasePassword != "")
	}
}

// TestLoadEnvSuwayomiDatabaseFields confirms all four embedded-Suwayomi DB
// fields are settable via environment variables.
func TestLoadEnvSuwayomiDatabaseFields(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_SUWAYOMI_DATABASETYPE", "POSTGRESQL")
	t.Setenv("TSUNDOKU_SUWAYOMI_DATABASEURL", "postgresql://db:5432/suwayomi")
	t.Setenv("TSUNDOKU_SUWAYOMI_DATABASEUSERNAME", "suwa")
	t.Setenv("TSUNDOKU_SUWAYOMI_DATABASEPASSWORD", "s3cr3t")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s := cfg.Suwayomi
	if s.DatabaseType != "POSTGRESQL" {
		t.Errorf("DatabaseType = %q, want POSTGRESQL", s.DatabaseType)
	}
	if s.DatabaseURL != "postgresql://db:5432/suwayomi" {
		t.Errorf("DatabaseURL = %q, want postgresql://db:5432/suwayomi", s.DatabaseURL)
	}
	if s.DatabaseUsername != "suwa" {
		t.Errorf("DatabaseUsername = %q, want suwa", s.DatabaseUsername)
	}
	if s.DatabasePassword != "s3cr3t" {
		t.Errorf("DatabasePassword = %q, want s3cr3t", s.DatabasePassword)
	}
}

// suwayomiDBConfig builds a Config valid except for the Suwayomi DB fields under
// test, so validate() exercises only the DB-selection rule.
func suwayomiDBConfig(dbType, dbURL string) *config.Config {
	return &config.Config{
		Database: config.DatabaseConfig{Password: "somepassword"},
		Auth:     config.AuthConfig{Secret: "exactly16charssss"},
		Suwayomi: config.SuwayomiConfig{DatabaseType: dbType, DatabaseURL: dbURL},
	}
}

// TestValidateSuwayomiDatabaseAccepts confirms validate() passes for the valid
// DB selections: blank (default H2), explicit H2 (no URL needed), and POSTGRESQL
// with a well-formed bare postgresql:// URL.
func TestValidateSuwayomiDatabaseAccepts(t *testing.T) {
	cases := []struct {
		name, dbType, dbURL string
	}{
		{"blank-default-h2", "", ""},
		{"explicit-h2-no-url", "H2", ""},
		{"postgres-valid-url", "POSTGRESQL", "postgresql://localhost:5432/suwayomi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := config.ExportValidateForTest(suwayomiDBConfig(tc.dbType, tc.dbURL)); err != nil {
				t.Errorf("validate() rejected %s/%q, want accept: %v", tc.dbType, tc.dbURL, err)
			}
		})
	}
}

// TestValidateSuwayomiDatabaseRejects confirms validate() fails closed for an
// unrecognised DatabaseType, POSTGRESQL with a missing URL, and POSTGRESQL with
// a malformed URL — including a jdbc:postgresql:// value (Suwayomi adds the
// jdbc: prefix itself, so a jdbc-prefixed value must be rejected).
func TestValidateSuwayomiDatabaseRejects(t *testing.T) {
	cases := []struct {
		name, dbType, dbURL, wantVar string
	}{
		{"unknown-type", "MYSQL", "", "TSUNDOKU_SUWAYOMI_DATABASETYPE"},
		{"postgres-missing-url", "POSTGRESQL", "", "TSUNDOKU_SUWAYOMI_DATABASEURL"},
		{"postgres-jdbc-prefixed", "POSTGRESQL", "jdbc:postgresql://h:5432/db", "TSUNDOKU_SUWAYOMI_DATABASEURL"},
		{"postgres-wrong-scheme", "POSTGRESQL", "http://h:5432/db", "TSUNDOKU_SUWAYOMI_DATABASEURL"},
		{"postgres-no-host", "POSTGRESQL", "postgresql:///db", "TSUNDOKU_SUWAYOMI_DATABASEURL"},
		{"postgres-garbage-url", "POSTGRESQL", "://nope", "TSUNDOKU_SUWAYOMI_DATABASEURL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ExportValidateForTest(suwayomiDBConfig(tc.dbType, tc.dbURL))
			if err == nil {
				t.Fatalf("validate() accepted %s/%q, want reject", tc.dbType, tc.dbURL)
			}
			if !strings.Contains(err.Error(), tc.wantVar) {
				t.Errorf("error for %s/%q should name %s, got: %v", tc.dbType, tc.dbURL, tc.wantVar, err)
			}
		})
	}
}

// TestLoadRejectsBadSuwayomiDatabase confirms the DB-selection rule is enforced
// end-to-end through Load() (not just the exported validate()): a POSTGRESQL
// type with no URL aborts startup.
func TestLoadRejectsBadSuwayomiDatabase(t *testing.T) {
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "x")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")
	t.Setenv("TSUNDOKU_SUWAYOMI_DATABASETYPE", "POSTGRESQL")
	// Deliberately omit TSUNDOKU_SUWAYOMI_DATABASEURL.

	if _, err := config.Load(); err == nil {
		t.Fatal("expected Load() to fail for POSTGRESQL without a DatabaseURL, got nil")
	}
}
