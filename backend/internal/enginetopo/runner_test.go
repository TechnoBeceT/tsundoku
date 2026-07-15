package enginetopo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

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
// work is done — no Preferences call — so a dead engine at boot produces one
// warning, not a wall of per-row failures. It also proves the goroutine returns
// (does not block) on the failure path.
func TestRunSeed_EngineUnreachableSkips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// One live provider that WOULD be preference-seeded if the passes ran.
	seedProvider(ctx, t, client, "Solo Leveling", "123", 42)

	fc := sourceenginefake.New(sourceenginefake.WithError("Sources", errors.New("connection refused")))
	deps := enginetopo.SeedDeps{
		Client:  fc,
		DB:      client,
		Cache:   apkcache.New(t.TempDir()),
		HTTPGet: nil, // never reached — Sources fails first
	}

	rep := runSeedInBackground(t, ctx, deps)
	if !rep.Skipped {
		t.Fatalf("report.Skipped = false, want true (engine unreachable)")
	}
	if got := fc.CallCount("Preferences"); got != 0 {
		t.Errorf("Preferences calls = %d, want 0 (every pass must be skipped)", got)
	}
}

// TestRunSeed_RunsEveryPass proves the happy path invokes both seed funcs: the
// preference seed captures a source preference, and the extension pass runs
// (against an empty-repo/empty-extension fake it legitimately caches nothing) —
// all panic-free and without blocking.
func TestRunSeed_RunsEveryPass(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "123", 42)

	fc := sourceenginefake.New(sourceenginefake.WithPreferences(123, []sourceengine.Preference{
		{Type: sourceengine.PreferenceEditText, Key: "lang", CurrentValue: "en"},
	}))
	deps := enginetopo.SeedDeps{
		Client:  fc,
		DB:      client,
		Cache:   apkcache.New(t.TempDir()),
		HTTPGet: nil, // no repos/extensions on the fake ⇒ never invoked
	}

	rep := runSeedInBackground(t, ctx, deps)
	if rep.Skipped {
		t.Fatalf("report.Skipped = true, want false (engine reachable)")
	}
	if rep.PrefsSeeded != 1 {
		t.Errorf("PrefsSeeded = %d, want 1 (preference pass ran)", rep.PrefsSeeded)
	}

	rows := client.SourcePreference.Query().AllX(ctx)
	if len(rows) != 1 || rows[0].Key != "lang" {
		t.Errorf("preference rows = %v, want one 'lang' row (preference pass persisted it)", rows)
	}
}
