// Package warmup_test exercises the warm-up Service against a fake engine-host
// client and a real metrics Service over an ephemeral PostgreSQL instance
// (testdb). Tests require Docker.
package warmup_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// fakeClient is a minimal sourceengine.Client: it embeds the interface (so the
// many unused methods exist but panic if called) and overrides only Sources +
// Popular, the two methods warm-up touches. It records the order of Popular
// calls to prove the pass is serial, and can be configured to fail specific
// sources by id.
type fakeClient struct {
	sourceengine.Client
	sources     []sourceengine.Source
	popularErrs map[int64]error // sourceID → error to return from Popular
	mu          sync.Mutex
	calls       []int64
}

func (f *fakeClient) Sources(context.Context) ([]sourceengine.Source, error) {
	return f.sources, nil
}

func (f *fakeClient) Popular(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sourceID)
	f.mu.Unlock()
	return sourceengine.SearchResult{}, f.popularErrs[sourceID]
}

// sid renders a source id the same way warmup.sourceIDString does, so tests
// key metrics rows/lookups exactly as the Service itself does.
func sid(id int64) string {
	return strconv.FormatInt(id, 10)
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

// TestWarmAll warms every enabled online source (a known-broken source
// excluded), serially in source order, and stamps last_warmed_at on each.
func TestWarmAll(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "EN", Lang: "en"},
			{ID: 2, Name: "KO", Lang: "ko"},
			{ID: 3, Name: "InfinityScans", Lang: "en"},
		},
	}
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil)

	n, err := svc.WarmAll(ctx)
	if err != nil {
		t.Fatalf("WarmAll: %v", err)
	}
	if n != 2 {
		t.Fatalf("warmed = %d, want 2 (known-broken source excluded)", n)
	}
	if len(fc.calls) != 2 || fc.calls[0] != 1 || fc.calls[1] != 2 {
		t.Errorf("Popular calls = %v, want [1 2] in order (serial)", fc.calls)
	}
	if !warmed(t, client, sid(1)) || !warmed(t, client, sid(2)) {
		t.Error("both online sources should have last_warmed_at stamped")
	}
}

// TestWarmSlow warms only never-measured OR slow sources (EWMA > threshold),
// never a fast measured source.
func TestWarmSlow(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	// Seed metrics: "fast" (id 1) is measured, under threshold, AND warmed
	// recently (so neither the slow arm nor the TTL arm selects it); "slow"
	// (id 2) is over threshold; "fresh" (id 3) has never been measured (no row).
	mustCreateWarmed(t, client, sid(1), 1000, time.Now().Add(-time.Minute))
	mustCreate(t, client, sid(2), 9000)

	fc := &fakeClient{sources: []sourceengine.Source{
		{ID: 1, Name: "Fast", Lang: "en"},
		{ID: 2, Name: "Slow", Lang: "en"},
		{ID: 3, Name: "Fresh", Lang: "en"},
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
		if id == 1 {
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
		sources: []sourceengine.Source{
			{ID: 1, Name: "A", Lang: "en"},
			{ID: 2, Name: "B", Lang: "en"},
		},
		popularErrs: map[int64]error{1: errors.New("boom")},
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
	if warmed(t, client, sid(1)) {
		t.Error("failing source 'a' should not be stamped warmed")
	}
	if !warmed(t, client, sid(2)) {
		t.Error("successful source 'b' should be stamped warmed")
	}
}

// TestWarmAll_SkipsGatedSource proves a source whose physical name is cooled
// down by the source-politeness gate (internal/sourcegate) is skipped
// entirely — no Popular call, not counted as warmed, not stamped — while an
// available source in the same pass still warms normally.
func TestWarmAll_SkipsGatedSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Blocked", Lang: "en"},
			{ID: 2, Name: "OK", Lang: "en"},
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
		if id == 1 {
			t.Errorf("blocked source must not be Popular'd; calls = %v", fc.calls)
		}
	}
	if warmed(t, client, sid(1)) {
		t.Error("blocked source should not be stamped warmed")
	}
	if !warmed(t, client, sid(2)) {
		t.Error("available source should be stamped warmed")
	}
}

// TestWarmAll_GateAvailableRunsNormally proves that with no breaker row at
// all, the gate never interferes with a normal warm pass.
func TestWarmAll_GateAvailableRunsNormally(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{sources: []sourceengine.Source{{ID: 1, Name: "EN", Lang: "en"}}}

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

// TestWarmSlow_StaleWarmSelectsFastButCold proves the TTL arm of WarmSlow: a
// source that is FAST (EWMA under threshold, so metrics.IsSlow is false) but was
// last warmed longer ago than sessionWarmTTL is still selected for warming (its
// anti-bot clearance may have lapsed), while an equally-fast source warmed
// recently is skipped.
func TestWarmSlow_StaleWarmSelectsFastButCold(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	now := time.Now()

	// "cold" (id 1): fast (under threshold) but warmed 20m ago (> 12m TTL) → must warm.
	mustCreateWarmed(t, client, sid(1), 1000, now.Add(-20*time.Minute))
	// "hot" (id 2): fast AND warmed 1m ago (< 12m TTL) → must be skipped.
	mustCreateWarmed(t, client, sid(2), 1000, now.Add(-1*time.Minute))

	fc := &fakeClient{sources: []sourceengine.Source{
		{ID: 1, Name: "Cold", Lang: "en"},
		{ID: 2, Name: "Hot", Lang: "en"},
	}}
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil)

	n, err := svc.WarmSlow(ctx)
	if err != nil {
		t.Fatalf("WarmSlow: %v", err)
	}
	if n != 1 {
		t.Fatalf("warmed = %d, want 1 (only the stale-but-fast source)", n)
	}
	if len(fc.calls) != 1 || fc.calls[0] != 1 {
		t.Errorf("Popular calls = %v, want [1] (fast-recently-warmed 'hot' skipped)", fc.calls)
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

// mustCreateWarmed seeds a measured metric row with the given EWMA latency and a
// last_warmed_at stamp, so WarmSlow's stale-warm (TTL) arm can be exercised.
func mustCreateWarmed(t *testing.T, client *ent.Client, sourceID string, ewmaMs int, warmedAt time.Time) {
	t.Helper()
	if err := client.SourceMetric.Create().
		SetSourceID(sourceID).
		SetSourceName(sourceID).
		SetEwmaLatencyMs(ewmaMs).
		SetSearchCount(1).
		SetSuccessCount(1).
		SetLastWarmedAt(warmedAt).
		Exec(context.Background()); err != nil {
		t.Fatalf("seed warmed metric %q: %v", sourceID, err)
	}
}
