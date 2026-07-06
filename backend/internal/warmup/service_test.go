// Package warmup_test exercises the warm-up Service against a fake Suwayomi
// client and a real metrics Service over an ephemeral PostgreSQL instance
// (testdb). Tests require Docker.
package warmup_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// fakeClient is a minimal suwayomi.Client: it embeds the interface (so the many
// unused methods exist but panic if called) and overrides only Sources + Browse,
// the two methods warm-up touches. It records the order of Browse calls to prove
// the pass is serial.
type fakeClient struct {
	suwayomi.Client
	sources    []suwayomi.Source
	browseErrs map[string]error // sourceID → error to return from Browse
	mu         sync.Mutex
	calls      []string
}

func (f *fakeClient) Sources(context.Context) ([]suwayomi.Source, error) {
	return f.sources, nil
}

func (f *fakeClient) Browse(_ context.Context, sourceID string, _ suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sourceID)
	f.mu.Unlock()
	return suwayomi.BrowseResult{}, f.browseErrs[sourceID]
}

// warmed reports whether a source has a last_warmed_at stamp.
func warmed(t *testing.T, client *ent.Client, sourceID string) bool {
	t.Helper()
	snap, err := metrics.NewService(client).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	m := snap[sourceID]
	return m != nil && m.LastWarmedAt != nil
}

// TestWarmAll warms every enabled online source (Local + disabled excluded),
// serially in source order, and stamps last_warmed_at on each.
func TestWarmAll(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "en-src", Name: "EN", Lang: "en"},
			{ID: "ko-src", Name: "KO", Lang: "ko"},
			{ID: suwayomi.LocalSourceID, Name: "Local", Lang: suwayomi.LocalSourceLang},
			{ID: "off-src", Name: "Off", Lang: "en", Disabled: true},
		},
	}
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil)

	n, err := svc.WarmAll(ctx)
	if err != nil {
		t.Fatalf("WarmAll: %v", err)
	}
	if n != 2 {
		t.Fatalf("warmed = %d, want 2 (Local + disabled excluded)", n)
	}
	if len(fc.calls) != 2 || fc.calls[0] != "en-src" || fc.calls[1] != "ko-src" {
		t.Errorf("Browse calls = %v, want [en-src ko-src] in order (serial)", fc.calls)
	}
	if !warmed(t, client, "en-src") || !warmed(t, client, "ko-src") {
		t.Error("both online sources should have last_warmed_at stamped")
	}
}

// TestWarmSlow warms only never-measured OR slow sources (EWMA > threshold),
// never a fast measured source.
func TestWarmSlow(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	// Seed metrics: "fast" is measured & under threshold; "slow" is over; "fresh"
	// has never been measured (no row).
	mustCreate(t, client, "fast", 1000)
	mustCreate(t, client, "slow", 9000)

	fc := &fakeClient{sources: []suwayomi.Source{
		{ID: "fast", Name: "Fast", Lang: "en"},
		{ID: "slow", Name: "Slow", Lang: "en"},
		{ID: "fresh", Name: "Fresh", Lang: "en"},
	}}
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil)

	n, err := svc.WarmSlow(ctx)
	if err != nil {
		t.Fatalf("WarmSlow: %v", err)
	}
	if n != 2 {
		t.Fatalf("warmed = %d, want 2 (slow + fresh)", n)
	}
	for _, id := range fc.calls {
		if id == "fast" {
			t.Errorf("fast source must not be warmed (under threshold); calls = %v", fc.calls)
		}
	}
}

// TestWarmSlow_OneFailureDoesNotAbort proves a failing source is logged + skipped
// (not counted) while the rest of the pass continues.
func TestWarmSlow_OneFailureDoesNotAbort(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "a", Name: "A", Lang: "en"},
			{ID: "b", Name: "B", Lang: "en"},
		},
		browseErrs: map[string]error{"a": errors.New("boom")},
	}
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil)

	n, err := svc.WarmSlow(ctx) // both never-measured ⇒ both slow
	if err != nil {
		t.Fatalf("WarmSlow: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("both sources should be attempted, calls = %v", fc.calls)
	}
	if n != 1 {
		t.Errorf("warmed = %d, want 1 (only the successful source counts)", n)
	}
	// The failing source recorded a sample but was NOT stamped warmed.
	if warmed(t, client, "a") {
		t.Error("failing source 'a' should not be stamped warmed")
	}
	if !warmed(t, client, "b") {
		t.Error("successful source 'b' should be stamped warmed")
	}
}

// TestWarmAll_SkipsGatedSource proves a source whose physical name is cooled
// down by the source-politeness gate (internal/sourcegate) is skipped
// entirely — no Browse call, not counted as warmed, not stamped — while an
// available source in the same pass still warms normally.
func TestWarmAll_SkipsGatedSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "blocked-src", Name: "Blocked", Lang: "en"},
			{ID: "ok-src", Name: "OK", Lang: "en"},
		},
	}

	// Pre-trip the breaker keyed by the source's NAME (not its id).
	client.SourceCircuitState.Create().
		SetSourceKey("Blocked").
		SetConsecutiveFailures(5).
		SetCooldownUntil(time.Now().Add(time.Hour)).
		SaveX(ctx)

	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 5, SourcesCooldownIv: time.Hour})
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, gate)

	n, err := svc.WarmAll(ctx)
	if err != nil {
		t.Fatalf("WarmAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("warmed = %d, want 1 (blocked source excluded)", n)
	}
	for _, id := range fc.calls {
		if id == "blocked-src" {
			t.Errorf("blocked source must not be Browse'd; calls = %v", fc.calls)
		}
	}
	if warmed(t, client, "blocked-src") {
		t.Error("blocked source should not be stamped warmed")
	}
	if !warmed(t, client, "ok-src") {
		t.Error("available source should be stamped warmed")
	}
}

// TestWarmAll_GateAvailableRunsNormally proves that with no breaker row at
// all, the gate never interferes with a normal warm pass.
func TestWarmAll_GateAvailableRunsNormally(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{sources: []suwayomi.Source{{ID: "en-src", Name: "EN", Lang: "en"}}}

	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 5, SourcesCooldownIv: time.Hour})
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, gate)

	n, err := svc.WarmAll(ctx)
	if err != nil {
		t.Fatalf("WarmAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("warmed = %d, want 1", n)
	}
}

// mustCreate seeds a measured metric row with the given EWMA latency.
func mustCreate(t *testing.T, client *ent.Client, sourceID string, ewmaMs int) {
	t.Helper()
	if err := client.SourceMetric.Create().
		SetSourceID(sourceID).
		SetSourceName(sourceID).
		SetEwmaLatencyMs(ewmaMs).
		SetSearchCount(1).
		SetSuccessCount(1).
		Exec(context.Background()); err != nil {
		t.Fatalf("seed metric %q: %v", sourceID, err)
	}
}
