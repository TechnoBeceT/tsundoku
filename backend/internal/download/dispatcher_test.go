// Package download_test contains integration tests for the download dispatcher.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// mustTempDir creates a temporary directory for test storage and registers its
// cleanup.
func mustTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tsundoku-dispatcher-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestDispatcher_HappyPath verifies that RunOnce on a single wanted chapter
// ends with state==downloaded and a CBZ file on disk.
func TestDispatcher_HappyPath(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Happy Series").SetSlug("happy-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-1").
		SetURL("https://mangadex.org/ch1").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)

	f := fake.New()
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 2,
		MaxRetries:             3,
		Storage:                storageDir,
	})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded, got %s", got.State)
	}
	if got.Filename == "" {
		t.Error("filename should be set after download")
	}
	if got.PageCount == nil || *got.PageCount != 5 {
		t.Errorf("page_count: want 5, got %v", got.PageCount)
	}
	if got.DownloadDate == nil {
		t.Error("download_date should be set after download")
	}

	// Verify the CBZ file exists on disk.
	cbzPath := filepath.Join(storageDir, "Other", "Happy Series", got.Filename)
	if _, err := os.Stat(cbzPath); err != nil {
		t.Errorf("CBZ file not found at %s: %v", cbzPath, err)
	}
}

// TestDispatcher_FailFirstThenSucceed verifies that a transient failure on the
// first attempt is recorded correctly (state=failed, retries=1) and that a
// second RunOnce call (once next_attempt_at has been reset to the past)
// succeeds, ending with state=downloaded.
func TestDispatcher_FailFirstThenSucceed(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Retry Series").SetSlug("retry-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-retry").
		SetURL("https://mangadex.org/ch-retry").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-retry").SaveX(ctx)

	f := fake.New(fake.WithFailFirst())
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,
		MaxRetries:             3,
		Storage:                storageDir,
		// Use zero backoff so the second RunOnce processes immediately.
		Backoff: func(_ int) time.Duration { return 0 },
	})

	// First run: should fail.
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	after1 := client.Chapter.GetX(ctx, ch.ID)
	if after1.State != entchapter.StateFailed {
		t.Errorf("after first run: want failed, got %s", after1.State)
	}
	if after1.Retries != 1 {
		t.Errorf("after first run: want retries=1, got %d", after1.Retries)
	}
	if after1.LastError == "" {
		t.Error("after first run: last_error should be set")
	}

	// Reset next_attempt_at so the second run processes the chapter.
	past := time.Now().Add(-1 * time.Hour)
	client.Chapter.UpdateOneID(ch.ID).SetNextAttemptAt(past).ExecX(ctx)

	// Second run: should succeed.
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	after2 := client.Chapter.GetX(ctx, ch.ID)
	if after2.State != entchapter.StateDownloaded {
		t.Errorf("after second run: want downloaded, got %s", after2.State)
	}
}

// TestDispatcher_PermanentFailure verifies that a chapter that exhausts its
// retry budget ends in state permanently_failed.
func TestDispatcher_PermanentFailure(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("PermFail Series").SetSlug("permfail-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-perm").
		SetURL("https://mangadex.org/ch-perm").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-perm").SaveX(ctx)

	alwaysErr := errors.New("permanent fetch error")
	f := fake.New(fake.WithError(alwaysErr))
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,
		MaxRetries:             1, // single attempt before permanently_failed
		Storage:                storageDir,
		Backoff:                func(_ int) time.Duration { return 0 },
	})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StatePermanentlyFailed {
		t.Errorf("state: want permanently_failed, got %s", got.State)
	}
	if got.LastError == "" {
		t.Error("last_error should be set on permanent failure")
	}
}

// countingFetcher wraps a base fetcher and records the peak concurrent
// inflight count for a given provider.
type countingFetcher struct {
	base     fetcher.ChapterFetcher
	mu       sync.Mutex
	inflight int64
	peak     int64
}

func (c *countingFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	cur := atomic.AddInt64(&c.inflight, 1)
	c.mu.Lock()
	if cur > c.peak {
		c.peak = cur
	}
	c.mu.Unlock()
	// Small sleep to ensure concurrency overlaps.
	time.Sleep(5 * time.Millisecond)
	atomic.AddInt64(&c.inflight, -1)
	return c.base.Fetch(ctx, ref)
}

// TestDispatcher_PerProviderConcurrency verifies that the number of concurrent
// downloads from a single provider never exceeds PerProviderConcurrency.
func TestDispatcher_PerProviderConcurrency(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	const cap = 2
	s := client.Series.Create().SetTitle("Concur Series").SetSlug("concur-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)

	// Create 5 chapters from the same provider. Each has a distinct integer key
	// and number so that CBZ filenames are unique and concurrent renders do not
	// collide on the same path.
	for i := range 5 {
		num := float64(i + 1)
		key := fmt.Sprintf("%d", i+1)
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(key).
			SetNillableNumber(&num).
			SetURL("https://mangadex.org/ch-" + key).
			SetProviderIndex(i).
			SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNillableNumber(&num).SaveX(ctx)
	}

	base := fake.New()
	cf := &countingFetcher{base: base}
	d := download.New(client, cf, hub, download.Config{
		PerProviderConcurrency: cap,
		MaxRetries:             3,
		Storage:                storageDir,
	})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if cf.peak > cap {
		t.Errorf("peak concurrency %d exceeded cap %d", cf.peak, cap)
	}
}

// TestDispatcher_SSEEvents verifies that RunOnce on a single wanted chapter
// emits a download.start event followed by a download.done event.
func TestDispatcher_SSEEvents(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	events, unsub := hub.Subscribe()
	defer unsub()

	s := client.Series.Create().SetTitle("SSE Series").SetSlug("sse-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-sse").
		SetURL("https://mangadex.org/ch-sse").
		SetProviderIndex(0).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("ch-sse").SaveX(ctx)

	f := fake.New()
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,
		MaxRetries:             3,
		Storage:                storageDir,
	})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Collect up to 2 events (start + done).
	var got []sse.Event
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev, ok := <-events:
			if !ok {
				goto done
			}
			got = append(got, ev)
		case <-timeout:
			goto done
		}
	}
done:

	if len(got) < 2 {
		t.Fatalf("want at least 2 SSE events, got %d", len(got))
	}
	if got[0].Type != "download.start" {
		t.Errorf("first event type: want download.start, got %q", got[0].Type)
	}
	if got[1].Type != "download.done" {
		t.Errorf("second event type: want download.done, got %q", got[1].Type)
	}
}

// TestDispatcher_BestProviderPicked verifies that when two SeriesProviders offer
// the same chapter key, the one with higher importance is used to satisfy the
// download (its ID is stored as satisfied_by_provider_id on the Chapter row).
func TestDispatcher_BestProviderPicked(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Best Pick Series").SetSlug("best-pick-series").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-low").SetImportance(5).SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-high").SetImportance(15).SaveX(ctx)

	const key = "ch-best-pick"
	client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey(key).SetURL("https://low.example.com/ch").SetProviderIndex(0).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey(key).SetURL("https://high.example.com/ch").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SaveX(ctx)

	f := fake.New()
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,
		MaxRetries:             3,
		Storage:                storageDir,
	})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded, got %s", got.State)
	}
	if got.SatisfiedByProviderID == nil {
		t.Fatal("satisfied_by_provider_id should be set")
	}
	if *got.SatisfiedByProviderID != spHigh.ID {
		t.Errorf("satisfied_by_provider_id: want %s (high importance), got %s", spHigh.ID, *got.SatisfiedByProviderID)
	}
	if got.SatisfiedImportance == nil || *got.SatisfiedImportance != 15 {
		t.Errorf("satisfied_importance: want 15, got %v", got.SatisfiedImportance)
	}
}

// TestDispatcher_SSEFail verifies that a permanent failure emits download.fail
// as the final SSE event.
func TestDispatcher_SSEFail(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	events, unsub := hub.Subscribe()
	defer unsub()

	s := client.Series.Create().SetTitle("SSE Fail Series").SetSlug("sse-fail-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-sse-fail").
		SetURL("https://mangadex.org/ch-sse-fail").
		SetProviderIndex(0).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("ch-sse-fail").SaveX(ctx)

	alwaysErr := errors.New("boom")
	f := fake.New(fake.WithError(alwaysErr))
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,
		MaxRetries:             1,
		Storage:                storageDir,
		Backoff:                func(_ int) time.Duration { return 0 },
	})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var got []sse.Event
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev, ok := <-events:
			if !ok {
				goto done
			}
			got = append(got, ev)
		case <-timeout:
			goto done
		}
	}
done:

	if len(got) < 2 {
		t.Fatalf("want at least 2 SSE events (start + fail), got %d", len(got))
	}
	if got[0].Type != "download.start" {
		t.Errorf("first event: want download.start, got %q", got[0].Type)
	}
	if got[1].Type != "download.fail" {
		t.Errorf("second event: want download.fail, got %q", got[1].Type)
	}
}

// Ensure uuid is used to keep the import — used in table-driven extensions.
var _ = uuid.Nil
