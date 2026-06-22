// Package config_test exercises the config package from the outside
// (black-box, per fleet standard §13).
package config_test

import (
	"net/url"
	"strings"
	"testing"

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
