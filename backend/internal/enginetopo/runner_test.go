package enginetopo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// runnerDefaults are the settings defaults the RunSeed engine-config pass writes
// against — only the flaresolverr group is exercised, but a full-ish set keeps
// the Service constructor honest.
func runnerDefaults() settings.Defaults {
	return settings.Defaults{
		DownloadInterval:       15 * time.Minute,
		RefreshInterval:        2 * time.Hour,
		RefreshConcurrency:     4,
		MaxRetries:             3,
		RetryBackoff:           time.Minute,
		StaleGraceDays:         14,
		FlareSolverrTimeout:    60,
		FlareSolverrSessionTTL: 15,
		EngineSocksPort:        1080,
		EngineSocksVersion:     5,
	}
}

// runSeedInBackground runs RunSeed on its own goroutine and fails the test if it
// does not return within the deadline — the "never blocks" half of the boot-
// goroutine contract (a blocking seed would hang the fire-and-forget goroutine
// forever). It returns the report so callers can assert the outcome.
func runSeedInBackground(t *testing.T, ctx context.Context, deps enginetopo.SeedDeps) enginetopo.SeedReport {
	t.Helper()
	done := make(chan enginetopo.SeedReport, 1)
	go func() { done <- enginetopo.RunSeed(ctx, deps) }()
	select {
	case rep := <-done:
		return rep
	case <-time.After(30 * time.Second):
		t.Fatal("RunSeed did not return within 30s — it blocked")
		return enginetopo.SeedReport{}
	}
}

// TestRunSeed_EngineUnreachableSkips proves the reachability gate: when the
// engine probe (Sources) fails, every pass is skipped (Skipped=true) and no seed
// work is done — no MangaMeta call, no ServerSettings call — so a dead engine at
// boot produces one warning, not a wall of per-row failures. It also proves the
// goroutine returns (does not block) on the failure path.
func TestRunSeed_EngineUnreachableSkips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// One live provider that WOULD be backfilled if the passes ran.
	seedProvider(ctx, t, client, "Solo Leveling", "123", 42)

	fc := &fakeClient{
		sourcesErr: errors.New("connection refused"),
		urls:       map[int]string{42: "https://x.test/manga"},
	}
	deps := enginetopo.SeedDeps{
		Client:   fc,
		DB:       client,
		Cache:    apkcache.New(t.TempDir()),
		Settings: settings.NewService(client, runnerDefaults()),
		HTTPGet:  nil, // never reached — Sources fails first
	}

	rep := runSeedInBackground(t, ctx, deps)
	if !rep.Skipped {
		t.Fatalf("report.Skipped = false, want true (engine unreachable)")
	}
	if rep.URLsFilled != 0 {
		t.Errorf("URLsFilled = %d, want 0 (no pass should have run)", rep.URLsFilled)
	}
	if got := fc.callCount(42); got != 0 {
		t.Errorf("MangaMeta calls = %d, want 0 (backfill must be skipped)", got)
	}
}

// TestRunSeed_RunsEveryPass proves the happy path invokes all four seed funcs:
// the URL backfill fills a live provider, the preference seed captures a source
// preference, and the engine-config seed writes the flaresolverr settings — all
// panic-free and without blocking. (The extension pass runs against an
// empty-repo/empty-extension fake, so it legitimately caches nothing.)
func TestRunSeed_RunsEveryPass(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "123", 42)

	fc := &fakeClient{
		urls: map[int]string{42: "https://solo.test/manga"},
		prefsBySource: map[string][]suwayomi.SourcePreference{
			"123": {
				{Type: suwayomi.PreferenceEditText, Position: 1, Key: "lang", CurrentString: stringPtr("en")},
			},
		},
		serverSettings: suwayomi.SuwayomiSettings{
			FlareSolverrEnabled:     true,
			FlareSolverrURL:         "http://flaresolverr.test:8191",
			FlareSolverrTimeout:     60,
			FlareSolverrSessionName: "tsundoku",
			FlareSolverrSessionTTL:  15,
		},
	}
	settingsSvc := settings.NewService(client, runnerDefaults())
	deps := enginetopo.SeedDeps{
		Client:   fc,
		DB:       client,
		Cache:    apkcache.New(t.TempDir()),
		Settings: settingsSvc,
		HTTPGet:  nil, // no repos/extensions on the fake ⇒ never invoked
	}

	rep := runSeedInBackground(t, ctx, deps)
	if rep.Skipped {
		t.Fatalf("report.Skipped = true, want false (engine reachable)")
	}
	if rep.URLsFilled != 1 {
		t.Errorf("URLsFilled = %d, want 1 (backfill pass ran)", rep.URLsFilled)
	}
	if rep.PrefsSeeded != 1 {
		t.Errorf("PrefsSeeded = %d, want 1 (preference pass ran)", rep.PrefsSeeded)
	}

	// The engine-config pass wrote the flaresolverr values into the settings
	// overlay — proving SeedEngineConfig ran end-to-end.
	if got := settingsSvc.FlareSolverrURL(ctx); got != "http://flaresolverr.test:8191" {
		t.Errorf("FlareSolverrURL = %q, want the seeded value (engine-config pass must have run)", got)
	}

	// And the backfill actually persisted the url.
	rows, err := client.SeriesProvider.Query().All(ctx)
	if err != nil {
		t.Fatalf("query providers: %v", err)
	}
	if len(rows) != 1 || rows[0].URL != "https://solo.test/manga" {
		t.Errorf("provider url = %v, want it filled by the backfill pass", rows)
	}
}
