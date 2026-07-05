// Package metrics_test exercises the metrics Service against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package metrics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourcemetric "github.com/technobecet/tsundoku/internal/ent/sourcemetric"
	"github.com/technobecet/tsundoku/internal/metrics"
)

// rowFor loads the single metric row for sourceID, failing the test if absent.
func rowFor(t *testing.T, client *ent.Client, sourceID string) *ent.SourceMetric {
	t.Helper()
	m, err := client.SourceMetric.Query().
		Where(entsourcemetric.SourceIDEQ(sourceID)).
		Only(context.Background())
	if err != nil {
		t.Fatalf("load metric %q: %v", sourceID, err)
	}
	return m
}

// TestRecord_FirstSampleSeeds proves a first successful observation creates the
// row, seeds the EWMA to the sample latency, and sets the success fields.
func TestRecord_FirstSampleSeeds(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	ctx := context.Background()

	svc.Record(ctx, "src-1", "MangaDex", 400*time.Millisecond, nil)

	m := rowFor(t, client, "src-1")
	if m.SourceName != "MangaDex" {
		t.Errorf("source_name = %q, want MangaDex", m.SourceName)
	}
	if m.EwmaLatencyMs != 400 {
		t.Errorf("ewma seed = %d, want 400", m.EwmaLatencyMs)
	}
	if m.LastLatencyMs != 400 {
		t.Errorf("last_latency = %d, want 400", m.LastLatencyMs)
	}
	if m.SearchCount != 1 || m.SuccessCount != 1 || m.FailCount != 0 {
		t.Errorf("counters = search:%d success:%d fail:%d, want 1/1/0", m.SearchCount, m.SuccessCount, m.FailCount)
	}
	if m.LastSuccessAt == nil {
		t.Error("last_success_at should be set")
	}
	if m.LastErrorAt != nil {
		t.Error("last_error_at should be nil on success")
	}
}

// TestRecord_BlendsSecondSample proves a second observation blends into the EWMA
// (0.3*sample + 0.7*prev) rather than replacing it.
func TestRecord_BlendsSecondSample(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	ctx := context.Background()

	svc.Record(ctx, "src-1", "MangaDex", 100*time.Millisecond, nil)
	svc.Record(ctx, "src-1", "MangaDex", 200*time.Millisecond, nil)

	m := rowFor(t, client, "src-1")
	if m.EwmaLatencyMs != 130 { // 0.3*200 + 0.7*100
		t.Errorf("blended ewma = %d, want 130", m.EwmaLatencyMs)
	}
	if m.SearchCount != 2 || m.SuccessCount != 2 {
		t.Errorf("counters = search:%d success:%d, want 2/2", m.SearchCount, m.SuccessCount)
	}
}

// TestRecord_FailureFeedsLatencyButNotSuccess proves a failed observation bumps
// fail_count, stores the error, and STILL feeds its (long) latency into the EWMA
// — a deadline-dropped slow source must register as slow.
func TestRecord_FailureFeedsLatencyButNotSuccess(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	ctx := context.Background()

	svc.Record(ctx, "cf-src", "SlowSource", 85*time.Second, errors.New("context deadline exceeded"))

	m := rowFor(t, client, "cf-src")
	if m.EwmaLatencyMs != 85000 {
		t.Errorf("ewma = %d, want 85000 (failed-but-slow latency seeds ewma)", m.EwmaLatencyMs)
	}
	if m.SearchCount != 1 || m.SuccessCount != 0 || m.FailCount != 1 {
		t.Errorf("counters = search:%d success:%d fail:%d, want 1/0/1", m.SearchCount, m.SuccessCount, m.FailCount)
	}
	if m.LastError != "context deadline exceeded" {
		t.Errorf("last_error = %q", m.LastError)
	}
	if m.LastErrorAt == nil {
		t.Error("last_error_at should be set on failure")
	}
	if m.LastSuccessAt != nil {
		t.Error("last_success_at should be nil (never succeeded)")
	}
}

// TestRecordBatch proves a batch records every sample in one call, mixing
// success and failure across distinct sources.
func TestRecordBatch(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	ctx := context.Background()

	svc.RecordBatch(ctx, []metrics.Sample{
		{SourceID: "a", SourceName: "A", Latency: 300 * time.Millisecond, Err: nil},
		{SourceID: "b", SourceName: "B", Latency: 9 * time.Second, Err: errors.New("boom")},
	})

	if got := rowFor(t, client, "a").EwmaLatencyMs; got != 300 {
		t.Errorf("a ewma = %d, want 300", got)
	}
	b := rowFor(t, client, "b")
	if b.EwmaLatencyMs != 9000 || b.FailCount != 1 {
		t.Errorf("b = ewma:%d fail:%d, want 9000/1", b.EwmaLatencyMs, b.FailCount)
	}
}

// TestSetWarmed proves the warm-up stamp upserts the row (creating it when the
// source has never been searched) and sets last_warmed_at without touching the
// counters.
func TestSetWarmed(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	ctx := context.Background()
	at := time.Now().Truncate(time.Second)

	if err := svc.SetWarmed(ctx, "fresh", "Fresh", at); err != nil {
		t.Fatalf("SetWarmed: %v", err)
	}
	m := rowFor(t, client, "fresh")
	if m.LastWarmedAt == nil || !m.LastWarmedAt.Equal(at) {
		t.Errorf("last_warmed_at = %v, want %v", m.LastWarmedAt, at)
	}
	if m.SearchCount != 0 {
		t.Errorf("search_count = %d, want 0 (warm must not bump counters)", m.SearchCount)
	}
}

// TestSnapshotAndList proves Snapshot returns a by-source-id map and List returns
// all rows sorted by EWMA descending (slowest first).
func TestSnapshotAndList(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	ctx := context.Background()

	svc.Record(ctx, "fast", "Fast", 200*time.Millisecond, nil)
	svc.Record(ctx, "slow", "Slow", 9*time.Second, nil)

	snap, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != 2 || snap["slow"].EwmaLatencyMs != 9000 {
		t.Fatalf("snapshot = %+v", snap)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].SourceID != "slow" || list[1].SourceID != "fast" {
		t.Errorf("List order = [%s, %s], want [slow, fast]", list[0].SourceID, list[1].SourceID)
	}
}

// TestRecord_BestEffortOnClosedClient proves recording against a closed client
// does NOT panic and does NOT return (best-effort: it logs and swallows).
func TestRecord_BestEffortOnClosedClient(t *testing.T) {
	client := testdb.New(t)
	svc := metrics.NewService(client)
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Must not panic; there is no error to observe (best-effort).
	svc.Record(context.Background(), "x", "X", time.Second, nil)
	svc.RecordBatch(context.Background(), []metrics.Sample{{SourceID: "y", SourceName: "Y", Latency: time.Second}})
}
