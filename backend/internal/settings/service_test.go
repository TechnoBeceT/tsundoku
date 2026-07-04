// Package settings_test exercises the runtime-tunable settings overlay against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package settings_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/settings"
)

// testDefaults mirrors the config defaults so resolution tests are meaningful.
func testDefaults() settings.Defaults {
	return settings.Defaults{
		DownloadInterval:       15 * time.Minute,
		RefreshInterval:        2 * time.Hour,
		RefreshConcurrency:     4,
		MaxRetries:             3,
		RetryBackoff:           time.Minute,
		StaleGraceDays:         14,
		ExtensionCheckInterval: 24 * time.Hour,
	}
}

// TestAccessorsReturnDefaultsWhenNoRow proves every typed accessor falls back to
// the injected config default when the Settings table has no override.
func TestAccessorsReturnDefaultsWhenNoRow(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.DownloadInterval(ctx); got != 15*time.Minute {
		t.Errorf("DownloadInterval default = %v, want 15m", got)
	}
	if got := svc.RefreshInterval(ctx); got != 2*time.Hour {
		t.Errorf("RefreshInterval default = %v, want 2h", got)
	}
	if got := svc.RefreshConcurrency(ctx); got != 4 {
		t.Errorf("RefreshConcurrency default = %d, want 4", got)
	}
	if got := svc.MaxRetries(ctx); got != 3 {
		t.Errorf("MaxRetries default = %d, want 3", got)
	}
	if got := svc.RetryBackoff(ctx); got != time.Minute {
		t.Errorf("RetryBackoff default = %v, want 1m", got)
	}
	if got := svc.StaleGraceDays(ctx); got != 14 {
		t.Errorf("StaleGraceDays default = %d, want 14", got)
	}
}

// TestSetThenResolveDuration proves a Set override is read back by the typed
// accessor (the read-at-use / hot-reload contract for durations).
func TestSetThenResolveDuration(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyDownloadInterval, "30m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := svc.DownloadInterval(ctx); got != 30*time.Minute {
		t.Errorf("after Set, DownloadInterval = %v, want 30m", got)
	}
}

// TestSetThenResolveInt proves an int override round-trips through the accessor.
func TestSetThenResolveInt(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyMaxRetries, "7"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := svc.MaxRetries(ctx); got != 7 {
		t.Errorf("after Set, MaxRetries = %d, want 7", got)
	}
}

// TestSetIsIdempotentUpsert proves a second Set on the same key updates rather
// than duplicating (the key is unique; the second value wins).
func TestSetIsIdempotentUpsert(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyRefreshConcurrency, "8"); err != nil {
		t.Fatalf("Set 1: %v", err)
	}
	if err := svc.Set(ctx, settings.KeyRefreshConcurrency, "16"); err != nil {
		t.Fatalf("Set 2: %v", err)
	}
	if got := svc.RefreshConcurrency(ctx); got != 16 {
		t.Errorf("after re-Set, RefreshConcurrency = %d, want 16", got)
	}
}

// TestSetUnknownKey proves a key outside the allowlist is rejected (the API
// never writes arbitrary keys).
func TestSetUnknownKey(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())

	err := svc.Set(context.Background(), "jobs.secret_backdoor", "1")
	if !errors.Is(err, settings.ErrUnknownSetting) {
		t.Fatalf("want ErrUnknownSetting, got %v", err)
	}
}

// TestSetInvalidValue proves out-of-bounds / unparseable values are rejected for
// each value shape, so the store never holds an invalid value (fail-closed).
func TestSetInvalidValue(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name, key, value string
	}{
		{"duration below min", settings.KeyDownloadInterval, "5s"},
		{"refresh below min", settings.KeyRefreshInterval, "1m"},
		{"unparseable duration", settings.KeyRetryBackoff, "soon"},
		{"backoff below min", settings.KeyRetryBackoff, "0s"},
		{"retries negative", settings.KeyMaxRetries, "-1"},
		// 0 is rejected: a source must always get at least one attempt, else the
		// attempts>=maxRetries rule would drive the whole library to permanently_failed.
		{"retries zero", settings.KeyMaxRetries, "0"},
		{"retries over max", settings.KeyMaxRetries, "21"},
		{"concurrency zero", settings.KeyRefreshConcurrency, "0"},
		{"concurrency over max", settings.KeyRefreshConcurrency, "33"},
		{"unparseable int", settings.KeyMaxRetries, "lots"},
		{"days over max", settings.KeyStaleGraceDays, "366"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Set(ctx, tc.key, tc.value)
			if !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("want ErrInvalidSetting, got %v", err)
			}
		})
	}

	// The rejected writes must not have persisted: the accessor still returns the
	// default.
	if got := svc.MaxRetries(ctx); got != 3 {
		t.Errorf("rejected writes leaked: MaxRetries = %d, want default 3", got)
	}
}

// TestSetManyAllOrNothing proves a batch with one bad key writes nothing.
func TestSetManyAllOrNothing(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	err := svc.SetMany(ctx, []settings.KeyValue{
		{Key: settings.KeyMaxRetries, Value: "9"},        // valid
		{Key: settings.KeyDownloadInterval, Value: "1s"}, // invalid (< 1m)
	})
	if !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("want ErrInvalidSetting, got %v", err)
	}
	// The valid update in the same batch must have been rolled back.
	if got := svc.MaxRetries(ctx); got != 3 {
		t.Errorf("partial batch write leaked: MaxRetries = %d, want default 3", got)
	}
}

// TestSetManyPersistsAll proves a fully-valid batch persists every update.
func TestSetManyPersistsAll(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	err := svc.SetMany(ctx, []settings.KeyValue{
		{Key: settings.KeyMaxRetries, Value: "9"},
		{Key: settings.KeyRetryBackoff, Value: "2m"},
	})
	if err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if got := svc.MaxRetries(ctx); got != 9 {
		t.Errorf("MaxRetries = %d, want 9", got)
	}
	if got := svc.RetryBackoff(ctx); got != 2*time.Minute {
		t.Errorf("RetryBackoff = %v, want 2m", got)
	}
}

// TestListReflectsDefaultsAndOverrides proves List returns the whole allowlist in
// stable order, with current=default until an override is set, then current=value.
func TestListReflectsDefaultsAndOverrides(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	list := svc.List(ctx)
	if len(list) != 7 {
		t.Fatalf("List len = %d, want 7", len(list))
	}
	// Stable order: first row is download_interval.
	if list[0].Key != settings.KeyDownloadInterval {
		t.Errorf("List[0].Key = %q, want %q", list[0].Key, settings.KeyDownloadInterval)
	}
	assertAllRowsAtDefault(t, list)

	if err := svc.Set(ctx, settings.KeyDownloadInterval, "45m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	dl := findSetting(t, svc.List(ctx), settings.KeyDownloadInterval)
	if dl.Value != "45m0s" {
		t.Errorf("download_interval value = %q, want 45m0s", dl.Value)
	}
	if dl.Default != "15m0s" {
		t.Errorf("download_interval default = %q, want 15m0s", dl.Default)
	}
}

// assertAllRowsAtDefault fails if any row's current value differs from its
// default or is missing type/unit metadata.
func assertAllRowsAtDefault(t *testing.T, list []settings.SettingDTO) {
	t.Helper()
	for _, row := range list {
		if row.Value != row.Default {
			t.Errorf("%s: current %q != default %q before any override", row.Key, row.Value, row.Default)
		}
		if row.Type == "" || row.Unit == "" {
			t.Errorf("%s: missing type %q / unit %q", row.Key, row.Type, row.Unit)
		}
	}
}

// findSetting returns the row with the given key (failing the test if absent).
func findSetting(t *testing.T, list []settings.SettingDTO, key string) settings.SettingDTO {
	t.Helper()
	for _, row := range list {
		if row.Key == key {
			return row
		}
	}
	t.Fatalf("setting %q not found in list", key)
	return settings.SettingDTO{}
}

// TestStaticProviderReturnsFixedValues proves the Static provider satisfies the
// accessor surface and returns its constructed values (used by consumer tests).
func TestStaticProviderReturnsFixedValues(t *testing.T) {
	ctx := context.Background()
	s := settings.Static{
		Download: time.Second, Refresh: 2 * time.Second, Concurrency: 2,
		Retries: 5, Backoff: 3 * time.Second, StaleGrace: 7,
		ExtCheck: 12 * time.Hour,
	}
	if s.DownloadInterval(ctx) != time.Second || s.RefreshInterval(ctx) != 2*time.Second ||
		s.RefreshConcurrency(ctx) != 2 || s.MaxRetries(ctx) != 5 ||
		s.RetryBackoff(ctx) != 3*time.Second || s.StaleGraceDays(ctx) != 7 ||
		s.ExtensionCheckInterval(ctx) != 12*time.Hour {
		t.Errorf("Static returned unexpected values: %+v", s)
	}
}

// TestExtensionCheckIntervalValidation proves the extension_check_interval key
// accepts 0 (disabled) and >= 1h, and rejects positive values below 1h.
func TestExtensionCheckIntervalValidation(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	// 0 / "0s" = disabled; must be accepted and canonicalize to "0s".
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "0"); err != nil {
		t.Fatalf("Set 0: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != 0 {
		t.Errorf("ExtensionCheckInterval after Set 0 = %v, want 0", got)
	}

	// "0s" is also valid (canonical form).
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "0s"); err != nil {
		t.Fatalf("Set 0s: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != 0 {
		t.Errorf("ExtensionCheckInterval after Set 0s = %v, want 0", got)
	}

	// Below 1h (but non-zero) must be rejected.
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "30m"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 30m: want ErrInvalidSetting, got %v", err)
	}

	// Exactly 1h must be accepted.
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "1h"); err != nil {
		t.Fatalf("Set 1h: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != time.Hour {
		t.Errorf("ExtensionCheckInterval after Set 1h = %v, want 1h", got)
	}

	// 24h must be accepted.
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "24h"); err != nil {
		t.Fatalf("Set 24h: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != 24*time.Hour {
		t.Errorf("ExtensionCheckInterval after Set 24h = %v, want 24h", got)
	}
}

// TestExtensionCheckIntervalDefaultAccessor proves ExtensionCheckInterval returns
// the config default (24h) when no DB override exists.
func TestExtensionCheckIntervalDefaultAccessor(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.ExtensionCheckInterval(ctx); got != 24*time.Hour {
		t.Errorf("ExtensionCheckInterval default = %v, want 24h", got)
	}
}
