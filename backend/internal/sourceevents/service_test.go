package sourceevents_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// TestLogBatch_PersistsEveryField writes a batch and reads it back, asserting
// every field round-trips — the enum type/status, duration, the derived
// error_message + error_category (from errorclass), items_count, and metadata.
func TestLogBatch_PersistsEveryField(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := sourceevents.NewService(client)

	count := 7
	svc.LogBatch(ctx, []sourceevents.Event{
		{
			SourceKey: "Comix", SourceID: "42", SourceName: "Comix", Language: "en",
			Type: sourceevents.EventSearch, Status: sourceevents.StatusSuccess,
			Duration: 1500 * time.Millisecond, ItemsCount: &count,
			Metadata: map[string]string{"keyword": "solo leveling"},
		},
		{
			SourceKey: "Asura", SourceID: "7", SourceName: "Asura Scans",
			Type: sourceevents.EventDownload, Status: sourceevents.StatusFailed,
			Duration: 2 * time.Second, Err: context.DeadlineExceeded,
		},
	})

	rows, err := client.SourceEvent.Query().Order(ent.Asc(entsourceevent.FieldSourceKey)).All(ctx)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d events, want 2", len(rows))
	}
	assertFailedDownload(t, rows[0]) // rows[0] = "Asura" (source_key ASC)
	assertSuccessSearch(t, rows[1])  // rows[1] = "Comix"
}

// assertFailedDownload checks the failed-download event round-trip: type/status,
// duration, the derived error_message + error_category, and an unset items_count.
func assertFailedDownload(t *testing.T, e *ent.SourceEvent) {
	t.Helper()
	if e.EventType != entsourceevent.EventTypeDownload || e.Status != entsourceevent.StatusFailed {
		t.Fatalf("failed event: type=%q status=%q", e.EventType, e.Status)
	}
	if e.DurationMs != 2000 {
		t.Fatalf("failed event duration_ms = %d, want 2000", e.DurationMs)
	}
	if e.ErrorMessage == nil || e.ErrorCategory == nil {
		t.Fatalf("failed event must carry error_message + error_category, got %v/%v", e.ErrorMessage, e.ErrorCategory)
	}
	if *e.ErrorCategory != "timeout" {
		t.Fatalf("failed event error_category = %q, want timeout", *e.ErrorCategory)
	}
	if e.ItemsCount != nil {
		t.Fatalf("failed event items_count should be nil (unset), got %v", *e.ItemsCount)
	}
}

// assertSuccessSearch checks the successful-search event round-trip: type/status,
// duration, nil error fields, items_count, metadata, language, and source_id.
func assertSuccessSearch(t *testing.T, e *ent.SourceEvent) {
	t.Helper()
	if e.EventType != entsourceevent.EventTypeSearch || e.Status != entsourceevent.StatusSuccess {
		t.Fatalf("success event: type=%q status=%q", e.EventType, e.Status)
	}
	if e.DurationMs != 1500 {
		t.Fatalf("success event duration_ms = %d, want 1500", e.DurationMs)
	}
	if e.ErrorMessage != nil || e.ErrorCategory != nil {
		t.Fatalf("success event must have nil error fields, got %v/%v", e.ErrorMessage, e.ErrorCategory)
	}
	assertSuccessSearchPayload(t, e)
}

// assertSuccessSearchPayload checks the optional/context fields of the success
// event: items_count, metadata, language, and source_id.
func assertSuccessSearchPayload(t *testing.T, e *ent.SourceEvent) {
	t.Helper()
	if e.ItemsCount == nil || *e.ItemsCount != 7 {
		t.Fatalf("success event items_count = %v, want 7", e.ItemsCount)
	}
	if e.Metadata["keyword"] != "solo leveling" {
		t.Fatalf("success event metadata keyword = %q, want 'solo leveling'", e.Metadata["keyword"])
	}
	if e.Language != "en" || e.SourceID != "42" {
		t.Fatalf("success event language/source_id = %q/%q", e.Language, e.SourceID)
	}
}

// TestLog_SingleEvent proves the convenience one-event form persists a row.
func TestLog_SingleEvent(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := sourceevents.NewService(client)

	svc.Log(ctx, sourceevents.Event{
		SourceKey: "Weeb", SourceName: "Weeb Central",
		Type: sourceevents.EventWarm, Status: sourceevents.StatusSuccess,
	})

	n, err := client.SourceEvent.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d events, want 1", n)
	}
}

// TestLogBatch_EmptyIsNoOp proves an empty batch writes nothing (and never
// issues a query).
func TestLogBatch_EmptyIsNoOp(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	sourceevents.NewService(client).LogBatch(ctx, nil)

	n, err := client.SourceEvent.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("got %d events, want 0", n)
	}
}

// TestLogBatch_BestEffortSwallowsDBError proves a write against a closed client
// logs + swallows the failure (returns nothing, never panics) — the best-effort
// posture that keeps audit bookkeeping from ever breaking the caller.
func TestLogBatch_BestEffortSwallowsDBError(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := sourceevents.NewService(client)
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	// Must not panic and must return normally against the now-closed client.
	svc.LogBatch(ctx, []sourceevents.Event{{SourceKey: "X", Type: sourceevents.EventSearch, Status: sourceevents.StatusFailed}})
	svc.Log(ctx, sourceevents.Event{SourceKey: "Y", Type: sourceevents.EventSearch, Status: sourceevents.StatusSuccess})
}

// TestPurgeOld_DeletesOnlyOldRows proves the retention purge deletes rows older
// than the cutoff and keeps newer ones, returning the deleted count.
func TestPurgeOld_DeletesOnlyOldRows(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := sourceevents.NewService(client)

	now := time.Now()
	// created_at is immutable-with-default, but the create builder still lets us
	// SET it explicitly for the seed (only the UPDATE path is blocked).
	mustCreate(t, ctx, client, "old-1", now.Add(-48*time.Hour))
	mustCreate(t, ctx, client, "old-2", now.Add(-40*time.Hour))
	mustCreate(t, ctx, client, "fresh", now.Add(-1*time.Hour))

	cutoff := now.Add(-24 * time.Hour)
	removed, err := svc.PurgeOld(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeOld: %v", err)
	}
	if removed != 2 {
		t.Fatalf("PurgeOld removed %d, want 2", removed)
	}

	rows, err := client.SourceEvent.Query().All(ctx)
	if err != nil {
		t.Fatalf("query remaining: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceKey != "fresh" {
		t.Fatalf("after purge want only 'fresh', got %+v", rows)
	}
}

// mustCreate seeds one event row at an explicit created_at.
func mustCreate(t *testing.T, ctx context.Context, client *ent.Client, key string, at time.Time) {
	t.Helper()
	if err := client.SourceEvent.Create().
		SetSourceKey(key).
		SetEventType(entsourceevent.EventTypeSearch).
		SetStatus(entsourceevent.StatusSuccess).
		SetCreatedAt(at).
		Exec(ctx); err != nil {
		t.Fatalf("seed event %q: %v", key, err)
	}
}
