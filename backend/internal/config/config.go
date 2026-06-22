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
	// Suwayomi holds connection settings for the Suwayomi manga server.
	// Fields are stubs for M0; Milestone 2 fills in the full integration.
	Suwayomi SuwayomiConfig
	// Storage holds library-path settings for downloaded chapters.
	Storage StorageConfig
}

// AuthConfig holds HMAC signing settings for the single-owner auth layer.
type AuthConfig struct {
	// Secret is the HMAC-SHA256 signing key for owner JWTs.
	// Must be at least 16 characters; validate() fails closed on startup when
	// this is empty or shorter, preventing all tokens from being forgeable.
	// Set via TSUNDOKU_AUTH_SECRET.
	Secret string
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

// SuwayomiConfig holds connection settings for the Suwayomi manga server.
// These are stubs for Milestone 0; the full Suwayomi integration lands in M2.
type SuwayomiConfig struct {
	// Host is the Suwayomi server host (default "localhost").
	Host string
	// Port is the Suwayomi server port (default "4567").
	Port string
	// BasePath is the Suwayomi API base path (default "/api").
	BasePath string
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
		"suwayomi.host":     "localhost",
		"suwayomi.port":     "4567",
		"suwayomi.basepath": "/api",
		"storage.folder":    "/data/manga",
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
//	TSUNDOKU_SERVER_PORT         → server.port
//	TSUNDOKU_DATABASE_HOST       → database.host
//	TSUNDOKU_DATABASE_PASSWORD   → database.password
//	TSUNDOKU_DATABASE_SSLMODE    → database.sslmode
//	TSUNDOKU_SUWAYOMI_BASEPATH   → suwayomi.basepath
//	TSUNDOKU_STORAGE_FOLDER      → storage.folder
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

	if len(errs) > 0 {
		return errors.New("config: invalid configuration: " + strings.Join(errs, "; "))
	}
	return nil
}
