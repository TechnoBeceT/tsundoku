package download_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sse"
)

// eventCaptureRecorder captures logged source events for assertion, tracking how
// many events arrived via LogBatch vs single Log calls so a test can prove the
// download path BATCHES per cycle rather than writing one row per attempt.
type eventCaptureRecorder struct {
	mu         sync.Mutex
	events     []sourceevents.Event
	logCalls   int
	batchCalls int
}

func (r *eventCaptureRecorder) Log(_ context.Context, event sourceevents.Event) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.logCalls++
	r.mu.Unlock()
}

func (r *eventCaptureRecorder) LogBatch(_ context.Context, events []sourceevents.Event) {
	r.mu.Lock()
	r.events = append(r.events, events...)
	r.batchCalls++
	r.mu.Unlock()
}

func (r *eventCaptureRecorder) all() []sourceevents.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sourceevents.Event(nil), r.events...)
}

// TestDownloadEvents_Success proves a successful download attempt logs one
// `download` event with success status, the canonical source_key, and the
// rendered page count as items_count.
func TestDownloadEvents_Success(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Ev Series").SetSlug("ev-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("42").SetProviderName("MangaDex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("ch-1").SetURL("https://x/ch1").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)

	rec := &eventCaptureRecorder{}
	d := download.New(client, fake.New(), hub, download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil).WithEventRecorder(rec)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	events := rec.all()
	if len(events) != 1 {
		t.Fatalf("got %d download events, want 1", len(events))
	}
	// The cycle must FLUSH ONE BATCH, not a per-attempt single Log (spec
	// Write-volume: one LogBatch per cycle per-source outcome).
	if rec.batchCalls != 1 || rec.logCalls != 0 {
		t.Fatalf("want 1 batch + 0 single Log calls, got batch=%d log=%d", rec.batchCalls, rec.logCalls)
	}
	assertSuccessDownloadEvent(t, events[0])
}

// assertSuccessDownloadEvent checks a successful download event's type/status,
// source identity, and page-count items_count.
func assertSuccessDownloadEvent(t *testing.T, e sourceevents.Event) {
	t.Helper()
	if e.Type != sourceevents.EventDownload || e.Status != sourceevents.StatusSuccess {
		t.Fatalf("event type/status = %q/%q, want download/success", e.Type, e.Status)
	}
	if e.SourceKey != "MangaDex" || e.SourceID != "42" {
		t.Fatalf("event source_key/source_id = %q/%q, want MangaDex/42", e.SourceKey, e.SourceID)
	}
	if e.ItemsCount == nil || *e.ItemsCount != 5 {
		t.Fatalf("event items_count = %v, want 5 (fake page count)", e.ItemsCount)
	}
}

// TestDownloadEvents_Failure proves a failed fetch logs a `download` event with
// failed status and the fetch cause.
func TestDownloadEvents_Failure(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Fail Series").SetSlug("fail-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("ch-x").SetURL("https://x/chx").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("ch-x").SaveX(ctx)

	rec := &eventCaptureRecorder{}
	d := download.New(client, fake.New(fake.WithError(errors.New("cloudflare challenge"))), hub,
		download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: 0}, nil).WithEventRecorder(rec)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	events := rec.all()
	if len(events) != 1 {
		t.Fatalf("got %d download events, want 1", len(events))
	}
	e := events[0]
	if e.Type != sourceevents.EventDownload || e.Status != sourceevents.StatusFailed {
		t.Fatalf("event type/status = %q/%q, want download/failed", e.Type, e.Status)
	}
	// Disk-style provider (no provider_name): source_id is "".
	if e.SourceKey != "mangadex" || e.SourceID != "" {
		t.Fatalf("event source_key/source_id = %q/%q, want mangadex/''", e.SourceKey, e.SourceID)
	}
	if e.Err == nil {
		t.Fatal("failed download event should carry its cause")
	}
}
