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
		DownloadInterval:        15 * time.Minute,
		DownloadConcurrency:     5,
		RefreshInterval:         2 * time.Hour,
		RefreshConcurrency:      4,
		MaxRetries:              3,
		RetryBackoff:            time.Minute,
		StaleGraceDays:          14,
		ExtensionCheckInterval:  24 * time.Hour,
		WarmupInterval:          15 * time.Minute,
		WarmupSlowThresholdMs:   5000,
		SearchCacheTTL:          time.Hour,
		ChapterCacheTTL:         time.Hour,
		SourcesFailureThreshold: 5,
		SourcesCooldown:         30 * time.Minute,
		SourcesMinRequestDelay:  500 * time.Millisecond,
		SuppressSplitParts:      true,
		TrackRetryInterval:      5 * time.Minute,
		MetadataAutoIdentify:    true,
		FlareSolverrEnabled:     false,
		FlareSolverrURL:         "",
		FlareSolverrTimeout:     60,
		FlareSolverrSessionName: "",
		FlareSolverrSessionTTL:  15,
		NotificationsEnabled:    true,
		EngineSocksEnabled:      false,
		EngineSocksHost:         "",
		EngineSocksPort:         1080,
		EngineSocksVersion:      5,
		RetainedVersions:        3,
	}
}

// TestRetainedVersions proves the extensions.retained_versions accessor returns
// the injected default, hot-reloads a valid Set override at use-time, and
// fail-closes an out-of-bounds value (bounds 1..20).
func TestRetainedVersions(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.RetainedVersions(ctx); got != 3 {
		t.Errorf("RetainedVersions default = %d, want 3", got)
	}
	if err := svc.Set(ctx, settings.KeyRetainedVersions, "7"); err != nil {
		t.Fatalf("Set(7): %v", err)
	}
	if got := svc.RetainedVersions(ctx); got != 7 {
		t.Errorf("after Set, RetainedVersions = %d, want 7 (read-at-use hot reload)", got)
	}
	if err := svc.Set(ctx, settings.KeyRetainedVersions, "0"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(0) below the min: err = %v, want ErrInvalidSetting", err)
	}
	if err := svc.Set(ctx, settings.KeyRetainedVersions, "21"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(21) above the max: err = %v, want ErrInvalidSetting", err)
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
	if got := svc.DownloadConcurrency(ctx); got != 5 {
		t.Errorf("DownloadConcurrency default = %d, want 5", got)
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
	if got := svc.TrackRetryInterval(ctx); got != 5*time.Minute {
		t.Errorf("TrackRetryInterval default = %v, want 5m", got)
	}
}

// TestSetThenResolveTrackRetryInterval proves a Set override on the new
// tracker-retry tunable round-trips through its typed accessor, mirroring
// TestSetThenResolveDuration for the other duration tunables.
func TestSetThenResolveTrackRetryInterval(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyTrackRetryInterval, "2m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := svc.TrackRetryInterval(ctx); got != 2*time.Minute {
		t.Errorf("after Set, TrackRetryInterval = %v, want 2m", got)
	}
	if err := svc.Set(ctx, settings.KeyTrackRetryInterval, "10s"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(10s) below the 30s floor: err = %v, want ErrInvalidSetting", err)
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
	if len(list) != 32 {
		t.Fatalf("List len = %d, want 32", len(list))
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
// default or is missing type metadata. Unit is required for every type except
// bool (a bool tunable has no unit of measure).
func assertAllRowsAtDefault(t *testing.T, list []settings.SettingDTO) {
	t.Helper()
	for _, row := range list {
		if row.Value != row.Default {
			t.Errorf("%s: current %q != default %q before any override", row.Key, row.Value, row.Default)
		}
		if row.Type == "" {
			t.Errorf("%s: missing type %q", row.Key, row.Type)
		}
		if row.Unit == "" && row.Type != string(settings.TypeBool) {
			t.Errorf("%s: missing unit %q", row.Key, row.Unit)
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
		Download: time.Second, DownloadConc: 6, Refresh: 2 * time.Second, Concurrency: 2,
		Retries: 5, Backoff: 3 * time.Second, StaleGrace: 7,
		ExtCheck: 12 * time.Hour, WarmupIv: 15 * time.Minute, WarmupSlow: 4000,
		SourcesFailureThresh: 8, SourcesCooldownIv: 45 * time.Minute, SourcesMinDelay: 750 * time.Millisecond,
		MetadataAutoIdentifyFlag: true,
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DownloadInterval", s.DownloadInterval(ctx), time.Second},
		{"DownloadConcurrency", s.DownloadConcurrency(ctx), 6},
		{"RefreshInterval", s.RefreshInterval(ctx), 2 * time.Second},
		{"RefreshConcurrency", s.RefreshConcurrency(ctx), 2},
		{"MaxRetries", s.MaxRetries(ctx), 5},
		{"RetryBackoff", s.RetryBackoff(ctx), 3 * time.Second},
		{"StaleGraceDays", s.StaleGraceDays(ctx), 7},
		{"ExtensionCheckInterval", s.ExtensionCheckInterval(ctx), 12 * time.Hour},
		{"WarmupInterval", s.WarmupInterval(ctx), 15 * time.Minute},
		{"WarmupSlowThresholdMs", s.WarmupSlowThresholdMs(ctx), 4000},
		{"SourcesFailureThreshold", s.SourcesFailureThreshold(ctx), 8},
		{"SourcesCooldown", s.SourcesCooldown(ctx), 45 * time.Minute},
		{"SourcesMinRequestDelay", s.SourcesMinRequestDelay(ctx), 750 * time.Millisecond},
		{"MetadataAutoIdentify", s.MetadataAutoIdentify(ctx), true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("Static.%s = %v, want %v", c.name, c.got, c.want)
		}
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

// TestWarmupInterval proves the warm-up interval accessor returns the default
// (15m) when unset, accepts 0 (disabled) and >= 1m, and rejects sub-1m values.
func TestWarmupInterval(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.WarmupInterval(ctx); got != 15*time.Minute {
		t.Errorf("WarmupInterval default = %v, want 15m", got)
	}
	if err := svc.Set(ctx, settings.KeyWarmupInterval, "0"); err != nil {
		t.Fatalf("Set 0: %v", err)
	}
	if got := svc.WarmupInterval(ctx); got != 0 {
		t.Errorf("WarmupInterval after Set 0 = %v, want 0 (disabled)", got)
	}
	if err := svc.Set(ctx, settings.KeyWarmupInterval, "30s"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 30s: want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeyWarmupInterval, "5m"); err != nil {
		t.Fatalf("Set 5m: %v", err)
	}
	if got := svc.WarmupInterval(ctx); got != 5*time.Minute {
		t.Errorf("WarmupInterval after Set 5m = %v, want 5m", got)
	}
}

// TestWarmupSlowThresholdMs proves the slow-threshold accessor returns the
// default (5000) when unset, accepts an in-bounds value, and rejects out-of-bounds.
func TestWarmupSlowThresholdMs(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.WarmupSlowThresholdMs(ctx); got != 5000 {
		t.Errorf("WarmupSlowThresholdMs default = %d, want 5000", got)
	}
	if err := svc.Set(ctx, settings.KeyWarmupSlowThresholdMs, "50"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 50 (below 100): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeyWarmupSlowThresholdMs, "8000"); err != nil {
		t.Fatalf("Set 8000: %v", err)
	}
	if got := svc.WarmupSlowThresholdMs(ctx); got != 8000 {
		t.Errorf("WarmupSlowThresholdMs after Set = %d, want 8000", got)
	}
}

// TestCacheTTLs proves the two interactive-cache TTL accessors return the default
// (1h) when unset, accept 0 (caching disabled) and >= 1s, reject sub-1s values,
// and reflect a valid override (the hot-reload contract the caches rely on).
func TestCacheTTLs(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name string
		key  string
		get  func(context.Context) time.Duration
	}{
		{"search", settings.KeySearchCacheTTL, svc.SearchCacheTTL},
		{"chapter", settings.KeyChapterCacheTTL, svc.ChapterCacheTTL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.get(ctx); got != time.Hour {
				t.Errorf("%s default = %v, want 1h", c.name, got)
			}
			// 0 = disabled is accepted.
			if err := svc.Set(ctx, c.key, "0"); err != nil {
				t.Fatalf("Set 0: %v", err)
			}
			if got := c.get(ctx); got != 0 {
				t.Errorf("%s after Set 0 = %v, want 0 (disabled)", c.name, got)
			}
			// A positive value below the 1s floor is rejected.
			if err := svc.Set(ctx, c.key, "500ms"); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set 500ms: want ErrInvalidSetting, got %v", err)
			}
			// A valid override round-trips.
			if err := svc.Set(ctx, c.key, "2h"); err != nil {
				t.Fatalf("Set 2h: %v", err)
			}
			if got := c.get(ctx); got != 2*time.Hour {
				t.Errorf("%s after Set 2h = %v, want 2h", c.name, got)
			}
		})
	}
}

// TestDownloadConcurrency proves the per-source download-concurrency accessor
// returns the default (5) when unset, rejects out-of-bounds values (below 1 /
// above 32), and reflects a valid override (the hot-reload contract the dispatcher
// relies on).
func TestDownloadConcurrency(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.DownloadConcurrency(ctx); got != 5 {
		t.Errorf("DownloadConcurrency default = %d, want 5", got)
	}
	if err := svc.Set(ctx, settings.KeyDownloadConcurrency, "0"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 0 (below 1): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeyDownloadConcurrency, "33"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 33 (above 32): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeyDownloadConcurrency, "8"); err != nil {
		t.Fatalf("Set 8: %v", err)
	}
	if got := svc.DownloadConcurrency(ctx); got != 8 {
		t.Errorf("DownloadConcurrency after Set = %d, want 8", got)
	}
}

// TestSourcesFailureThreshold proves the circuit-breaker trip-threshold accessor
// returns the default (5) when unset, rejects a value below 1, and reflects a
// valid override (source-politeness Task 2).
func TestSourcesFailureThreshold(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.SourcesFailureThreshold(ctx); got != 5 {
		t.Errorf("SourcesFailureThreshold default = %d, want 5", got)
	}
	if err := svc.Set(ctx, settings.KeySourcesFailureThreshold, "0"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 0 (below 1): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeySourcesFailureThreshold, "3"); err != nil {
		t.Fatalf("Set 3: %v", err)
	}
	if got := svc.SourcesFailureThreshold(ctx); got != 3 {
		t.Errorf("SourcesFailureThreshold after Set = %d, want 3", got)
	}
}

// TestSourcesCooldown proves the circuit-breaker cooldown accessor returns the
// default (30m) when unset, rejects a value below 1m, and reflects a valid
// override.
func TestSourcesCooldown(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.SourcesCooldown(ctx); got != 30*time.Minute {
		t.Errorf("SourcesCooldown default = %v, want 30m", got)
	}
	if err := svc.Set(ctx, settings.KeySourcesCooldown, "30s"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 30s (below 1m): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeySourcesCooldown, "10m"); err != nil {
		t.Fatalf("Set 10m: %v", err)
	}
	if got := svc.SourcesCooldown(ctx); got != 10*time.Minute {
		t.Errorf("SourcesCooldown after Set = %v, want 10m", got)
	}
}

// TestSourcesMinRequestDelay_Default proves the politeness-delay accessor
// returns the config default (500ms) when the Settings table has no override.
func TestSourcesMinRequestDelay_Default(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())

	if got := svc.SourcesMinRequestDelay(context.Background()); got != 500*time.Millisecond {
		t.Errorf("SourcesMinRequestDelay default = %v, want 500ms", got)
	}
}

// TestSourcesMinRequestDelay_SetValidation is table-driven over the shapes the
// politeness delay must accept or reject: 0 (disabled), an arbitrary positive
// duration, and a rejected negative duration — proving the resolved accessor
// value matches what was stored for every accepted case.
func TestSourcesMinRequestDelay_SetValidation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    time.Duration
	}{
		{name: "zero disables politeness", raw: "0", want: 0},
		{name: "arbitrary positive duration", raw: "1200ms", want: 1200 * time.Millisecond},
		{name: "negative duration rejected", raw: "-5s", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testdb.New(t)
			svc := settings.NewService(db, testDefaults())
			ctx := context.Background()

			err := svc.Set(ctx, settings.KeySourcesMinRequestDelay, tc.raw)
			if tc.wantErr {
				if !errors.Is(err, settings.ErrInvalidSetting) {
					t.Fatalf("Set(%q): want ErrInvalidSetting, got %v", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tc.raw, err)
			}
			if got := svc.SourcesMinRequestDelay(ctx); got != tc.want {
				t.Errorf("SourcesMinRequestDelay after Set(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestSuppressSplitParts_DefaultAndOverride proves the fractional-part-
// suppression flag defaults to the injected value, round-trips through Set,
// and rejects a non-boolean value (fail-closed).
func TestSuppressSplitParts_DefaultAndOverride(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if !svc.SuppressSplitParts(ctx) {
		t.Fatal("default SuppressSplitParts = false, want true")
	}
	if err := svc.Set(ctx, settings.KeySuppressSplitParts, "false"); err != nil {
		t.Fatalf("Set false: %v", err)
	}
	if svc.SuppressSplitParts(ctx) {
		t.Fatal("after Set false, SuppressSplitParts = true, want false")
	}
	if err := svc.Set(ctx, settings.KeySuppressSplitParts, "notabool"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set invalid bool: want ErrInvalidSetting, got %v", err)
	}
}

// TestNotificationsEnabled_DefaultAndOverride proves the notifications.enabled
// tunable defaults to true, round-trips a false override, and rejects a
// non-boolean value (fail-closed) — mirroring the other bool tunables.
func TestNotificationsEnabled_DefaultAndOverride(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if !svc.NotificationsEnabled(ctx) {
		t.Fatal("default NotificationsEnabled = false, want true")
	}
	if err := svc.Set(ctx, settings.KeyNotificationsEnabled, "false"); err != nil {
		t.Fatalf("Set false: %v", err)
	}
	if svc.NotificationsEnabled(ctx) {
		t.Fatal("after Set false, NotificationsEnabled = true, want false")
	}
	if err := svc.Set(ctx, settings.KeyNotificationsEnabled, "notabool"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set invalid bool: want ErrInvalidSetting, got %v", err)
	}
}

// TestMetadataAutoIdentify_DefaultAndOverride proves the metadata.auto_identify
// tunable defaults to true and round-trips a Set override, mirroring
// TestSuppressSplitParts_DefaultAndOverride for the other bool tunable.
func TestMetadataAutoIdentify_DefaultAndOverride(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if !svc.MetadataAutoIdentify(ctx) {
		t.Fatal("default MetadataAutoIdentify = false, want true")
	}
	if err := svc.Set(ctx, settings.KeyMetadataAutoIdentify, "false"); err != nil {
		t.Fatalf("Set false: %v", err)
	}
	if svc.MetadataAutoIdentify(ctx) {
		t.Fatal("after Set false, MetadataAutoIdentify = true, want false")
	}
	if err := svc.Set(ctx, settings.KeyMetadataAutoIdentify, "notabool"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set invalid bool: want ErrInvalidSetting, got %v", err)
	}
}

// TestFlareSolverrDefaults proves every FlareSolverr accessor returns its
// injected default when the Settings table has no override (QCAT-238 —
// Tsundoku-owned Cloudflare-bypass config).
func TestFlareSolverrDefaults(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if svc.FlareSolverrEnabled(ctx) {
		t.Error("FlareSolverrEnabled default = true, want false")
	}
	if got := svc.FlareSolverrURL(ctx); got != "" {
		t.Errorf("FlareSolverrURL default = %q, want \"\"", got)
	}
	if got := svc.FlareSolverrTimeout(ctx); got != 60 {
		t.Errorf("FlareSolverrTimeout default = %d, want 60", got)
	}
	if got := svc.FlareSolverrSessionName(ctx); got != "" {
		t.Errorf("FlareSolverrSessionName default = %q, want \"\"", got)
	}
	if got := svc.FlareSolverrSessionTTL(ctx); got != 15 {
		t.Errorf("FlareSolverrSessionTTL default = %d, want 15", got)
	}
	if svc.FlareSolverrResponseFallback(ctx) {
		t.Error("FlareSolverrResponseFallback default = true, want false")
	}
}

// TestFlareSolverrSetAndResolve_Enabled proves the enabled flag round-trips.
func TestFlareSolverrSetAndResolve_Enabled(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrEnabled, "true"); err != nil {
		t.Fatalf("Set enabled: %v", err)
	}
	if !svc.FlareSolverrEnabled(ctx) {
		t.Error("after Set true, FlareSolverrEnabled = false")
	}
}

// TestFlareSolverrSetAndResolve_URL proves the URL round-trips, including
// clearing it back to blank (blank is always legal — "not configured").
func TestFlareSolverrSetAndResolve_URL(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, "http://flaresolverr:8191"); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if got := svc.FlareSolverrURL(ctx); got != "http://flaresolverr:8191" {
		t.Errorf("FlareSolverrURL after Set = %q, want http://flaresolverr:8191", got)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, ""); err != nil {
		t.Fatalf("Set url blank: %v", err)
	}
	if got := svc.FlareSolverrURL(ctx); got != "" {
		t.Errorf("FlareSolverrURL after Set \"\" = %q, want \"\"", got)
	}
}

// TestFlareSolverrSetAndResolve_TimeoutAndSession proves the timeout, session
// name (trimmed), and session TTL round-trip through their accessors.
func TestFlareSolverrSetAndResolve_TimeoutAndSession(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "120"); err != nil {
		t.Fatalf("Set timeout: %v", err)
	}
	if got := svc.FlareSolverrTimeout(ctx); got != 120 {
		t.Errorf("FlareSolverrTimeout after Set = %d, want 120", got)
	}

	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionName, "  tsundoku  "); err != nil {
		t.Fatalf("Set session name: %v", err)
	}
	if got := svc.FlareSolverrSessionName(ctx); got != "tsundoku" {
		t.Errorf("FlareSolverrSessionName after Set = %q, want trimmed \"tsundoku\"", got)
	}

	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionTTL, "30"); err != nil {
		t.Fatalf("Set session ttl: %v", err)
	}
	if got := svc.FlareSolverrSessionTTL(ctx); got != 30 {
		t.Errorf("FlareSolverrSessionTTL after Set = %d, want 30", got)
	}
}

// TestFlareSolverrSetAndResolve_ResponseFallback proves the
// asResponseFallback mirror flag round-trips.
func TestFlareSolverrSetAndResolve_ResponseFallback(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrResponseFallback, "true"); err != nil {
		t.Fatalf("Set response fallback: %v", err)
	}
	if !svc.FlareSolverrResponseFallback(ctx) {
		t.Error("after Set true, FlareSolverrResponseFallback = false")
	}
}

// TestFlareSolverrURLValidation proves the URL tunable accepts blank or a
// well-formed absolute http(s) URL and rejects everything else (a relative
// path, a non-http(s) scheme, or a hostless URL).
func TestFlareSolverrURLValidation(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"blank clears", "", false},
		{"valid http", "http://flaresolverr:8191", false},
		{"valid https", "https://flaresolverr.example.com", false},
		{"relative path rejected", "/flaresolverr", true},
		{"non-http scheme rejected", "ftp://flaresolverr:8191", true},
		{"hostless rejected", "http://", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Set(ctx, settings.KeyFlareSolverrURL, tc.raw)
			if tc.wantErr {
				if !errors.Is(err, settings.ErrInvalidSetting) {
					t.Fatalf("Set(%q): want ErrInvalidSetting, got %v", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tc.raw, err)
			}
		})
	}
}

// TestFlareSolverrIntBounds proves the timeout (5..600s) and session-ttl
// (0..1440m) tunables reject out-of-bounds values.
func TestFlareSolverrIntBounds(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name, key, value string
	}{
		{"timeout below min", settings.KeyFlareSolverrTimeout, "4"},
		{"timeout above max", settings.KeyFlareSolverrTimeout, "601"},
		{"timeout unparseable", settings.KeyFlareSolverrTimeout, "soon"},
		{"session ttl negative", settings.KeyFlareSolverrSessionTTL, "-1"},
		{"session ttl above max", settings.KeyFlareSolverrSessionTTL, "1441"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Set(ctx, tc.key, tc.value); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set(%q, %q): want ErrInvalidSetting, got %v", tc.key, tc.value, err)
			}
		})
	}

	// Bounds edges are accepted.
	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "5"); err != nil {
		t.Fatalf("Set timeout=5 (min): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "600"); err != nil {
		t.Fatalf("Set timeout=600 (max): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionTTL, "0"); err != nil {
		t.Fatalf("Set sessionTtl=0 (min): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionTTL, "1440"); err != nil {
		t.Fatalf("Set sessionTtl=1440 (max): %v", err)
	}
}

// TestEngineSocksDefaults proves every engine.socks_* accessor returns its
// injected default when the Settings table has no override.
func TestEngineSocksDefaults(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if svc.EngineSocksEnabled(ctx) {
		t.Error("EngineSocksEnabled default = true, want false")
	}
	if got := svc.EngineSocksHost(ctx); got != "" {
		t.Errorf("EngineSocksHost default = %q, want \"\"", got)
	}
	if got := svc.EngineSocksPort(ctx); got != 1080 {
		t.Errorf("EngineSocksPort default = %d, want 1080", got)
	}
	if got := svc.EngineSocksVersion(ctx); got != 5 {
		t.Errorf("EngineSocksVersion default = %d, want 5", got)
	}
}

// TestEngineSocksSetAndResolve proves every engine.socks_* tunable round-trips
// through Set + its typed accessor.
func TestEngineSocksSetAndResolve(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyEngineSocksEnabled, "true"); err != nil {
		t.Fatalf("Set enabled: %v", err)
	}
	if !svc.EngineSocksEnabled(ctx) {
		t.Error("after Set true, EngineSocksEnabled = false")
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksHost, "  socks.internal  "); err != nil {
		t.Fatalf("Set host: %v", err)
	}
	if got := svc.EngineSocksHost(ctx); got != "socks.internal" {
		t.Errorf("EngineSocksHost after Set = %q, want trimmed \"socks.internal\"", got)
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksPort, "1081"); err != nil {
		t.Fatalf("Set port: %v", err)
	}
	if got := svc.EngineSocksPort(ctx); got != 1081 {
		t.Errorf("EngineSocksPort after Set = %d, want 1081", got)
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksVersion, "4"); err != nil {
		t.Fatalf("Set version=4: %v", err)
	}
	if got := svc.EngineSocksVersion(ctx); got != 4 {
		t.Errorf("EngineSocksVersion after Set = %d, want 4", got)
	}
}

// TestEngineSocksPortBounds proves the port tunable rejects values outside
// [1, 65535].
func TestEngineSocksPortBounds(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct{ name, value string }{
		{"below min", "0"},
		{"above max", "65536"},
		{"unparseable", "many"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Set(ctx, settings.KeyEngineSocksPort, tc.value); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set(port, %q): want ErrInvalidSetting, got %v", tc.value, err)
			}
		})
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksPort, "1"); err != nil {
		t.Fatalf("Set port=1 (min): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyEngineSocksPort, "65535"); err != nil {
		t.Fatalf("Set port=65535 (max): %v", err)
	}
}

// TestEngineSocksVersionMustBe4Or5 proves the version tunable accepts ONLY 4
// or 5 — not a contiguous range like every other bounded int tunable.
func TestEngineSocksVersionMustBe4Or5(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct{ name, value string }{
		{"zero", "0"},
		{"three", "3"},
		{"six", "6"},
		{"unparseable", "five"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Set(ctx, settings.KeyEngineSocksVersion, tc.value); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set(version, %q): want ErrInvalidSetting, got %v", tc.value, err)
			}
		})
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksVersion, "4"); err != nil {
		t.Fatalf("Set version=4: %v", err)
	}
	if err := svc.Set(ctx, settings.KeyEngineSocksVersion, "5"); err != nil {
		t.Fatalf("Set version=5: %v", err)
	}
}

// TestExistingKeys proves the gap-detection reader: it returns exactly the
// queried keys that already have an explicit Settings row (owned by Tsundoku),
// omits keys that are still unset (resolving to their default), never reports a
// key that was not asked about, and short-circuits an empty query.
func TestExistingKeys(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	// Give two keys explicit rows; a third is left unset.
	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, "http://fs.example:8191"); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "90"); err != nil {
		t.Fatalf("Set timeout: %v", err)
	}

	got, err := svc.ExistingKeys(ctx, []string{
		settings.KeyFlareSolverrURL,
		settings.KeyFlareSolverrTimeout,
		settings.KeyFlareSolverrEnabled, // unset → must be absent
	})
	if err != nil {
		t.Fatalf("ExistingKeys: %v", err)
	}
	if !got[settings.KeyFlareSolverrURL] {
		t.Error("KeyFlareSolverrURL missing from ExistingKeys, want present (it has a row)")
	}
	if !got[settings.KeyFlareSolverrTimeout] {
		t.Error("KeyFlareSolverrTimeout missing from ExistingKeys, want present (it has a row)")
	}
	if got[settings.KeyFlareSolverrEnabled] {
		t.Error("KeyFlareSolverrEnabled present in ExistingKeys, want absent (it has no row)")
	}

	// An empty query short-circuits to an empty, non-nil set with no error.
	empty, err := svc.ExistingKeys(ctx, nil)
	if err != nil {
		t.Fatalf("ExistingKeys(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ExistingKeys(nil) = %v, want empty", empty)
	}
}
