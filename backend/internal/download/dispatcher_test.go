// Package download_test contains integration tests for the download dispatcher.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
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

// catID resolves a seeded default category's id by name (testdb seeds them).
func catID(ctx context.Context, db *ent.Client, name string) uuid.UUID {
	id, err := category.IDByName(ctx, db, name)
	if err != nil {
		panic(fmt.Sprintf("catID %q: %v", name, err))
	}
	return id
}

// TestDispatcher_HappyPath verifies that RunOnce on a single wanted chapter
// ends with state==downloaded, a CBZ file on disk, and all provenance fields set
// (§16 full-payload: satisfied_by_provider_id + satisfied_importance verified).
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

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	assertSuccessProvenance(t, got.State, got.Filename, got.PageCount, got.DownloadDate,
		got.SatisfiedByProviderID, got.SatisfiedImportance, sp.ID, sp.Importance, 5)

	// Verify the CBZ file exists on disk.
	cbzPath := filepath.Join(storageDir, "Other", "Happy Series", got.Filename)
	if _, statErr := os.Stat(cbzPath); statErr != nil {
		t.Errorf("CBZ file not found at %s: %v", cbzPath, statErr)
	}
}

// assertSuccessProvenance validates all provenance fields that the download
// success path must set (§16 full-payload).
func assertSuccessProvenance(
	t *testing.T,
	state entchapter.State,
	filename string,
	pageCount *int,
	downloadDate *time.Time,
	satisfiedByProviderID *uuid.UUID,
	satisfiedImportance *int,
	wantProviderID uuid.UUID,
	wantImportance int,
	wantPageCount int,
) {
	t.Helper()
	if state != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded, got %s", state)
	}
	if filename == "" {
		t.Error("filename should be set after download")
	}
	if pageCount == nil || *pageCount != wantPageCount {
		t.Errorf("page_count: want %d, got %v", wantPageCount, pageCount)
	}
	if downloadDate == nil {
		t.Error("download_date should be set after download")
	}
	// §16 provenance fields.
	if satisfiedByProviderID == nil {
		t.Error("satisfied_by_provider_id should be set after download")
	} else if *satisfiedByProviderID != wantProviderID {
		t.Errorf("satisfied_by_provider_id: want %s, got %s", wantProviderID, *satisfiedByProviderID)
	}
	if satisfiedImportance == nil {
		t.Error("satisfied_importance should be set after download")
	} else if *satisfiedImportance != wantImportance {
		t.Errorf("satisfied_importance: want %d, got %v", wantImportance, *satisfiedImportance)
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

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: 0})

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

		Storage: storageDir,
	}, settings.Static{Retries: 1, Backoff: 0})

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

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

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

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

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

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

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

		Storage: storageDir,
	}, settings.Static{Retries: 1, Backoff: 0})

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

// TestDispatcher_NoChapterStrandedInDownloading asserts that after RunOnce
// completes (for both fetch failures and permanent failures), no chapter remains
// in the downloading state. This is the regression test for the "stuck in
// downloading" bugs: provenance-update failure and SetState(downloaded) failure
// are DB-only paths, but the fetch-error path exercises the same handleFailure
// code that guards against stranding.
func TestDispatcher_NoChapterStrandedInDownloading(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("NoStrand Series").SetSlug("nostrand-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-nostrand").
		SetURL("https://mangadex.org/ch-nostrand").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-nostrand").SaveX(ctx)

	// Use an always-failing fetcher so handleFailure is exercised.
	f := fake.New(fake.WithError(errors.New("forced failure")))
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,

		Storage: storageDir,
	}, settings.Static{Retries: 2, Backoff: 0})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State == entchapter.StateDownloading {
		t.Errorf("chapter %s is stranded in downloading state after RunOnce", ch.ID)
	}
}

// TestDispatcher_ZeroPadding asserts that when a series has a high-numbered
// chapter, a low-numbered chapter's CBZ filename is zero-padded to match the
// width of the highest chapter number.
func TestDispatcher_ZeroPadding(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Pad Series").SetSlug("pad-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)

	// Chapter 5 is the download target; chapter 120 sets the series max (so the
	// integer part of "5" must be padded to "005").
	num5 := 5.0
	num120 := 120.0
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-5").
		SetNillableNumber(&num5).
		SetURL("https://mangadex.org/ch-5").
		SetProviderIndex(0).
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-120").
		SetNillableNumber(&num120).
		SetURL("https://mangadex.org/ch-120").
		SetProviderIndex(1).
		SaveX(ctx)

	// Only set chapter 5 to wanted; chapter 120 exists only as a ProviderChapter
	// to establish the series max — no Chapter row needed for it.
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-5").SetNillableNumber(&num5).SaveX(ctx)

	f := fake.New()
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded, got %s", got.State)
	}
	if got.Filename == "" {
		t.Fatal("filename should be set after download")
	}
	// The chapter number "5" with series max 120 must be zero-padded to "005".
	if !strings.Contains(got.Filename, "005") {
		t.Errorf("filename %q: expected zero-padded chapter number '005' (max=%v)", got.Filename, num120)
	}
}

// TestDispatcher_NoProviderStaysWanted verifies that a Chapter with no matching
// ProviderChapter is skipped during RunOnce: it must stay in wanted state and a
// download.skip SSE notice must be emitted. This branch is near-defensive —
// the ingest invariant always creates a ProviderChapter alongside each Chapter —
// but it is reachable in tests by constructing a Chapter without any
// ProviderChapter row.
func TestDispatcher_NoProviderStaysWanted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	events, unsub := hub.Subscribe()
	defer unsub()

	s := client.Series.Create().SetTitle("No-Provider Series").SetSlug("no-provider-series").SaveX(ctx)
	// Intentionally create a Chapter with NO matching ProviderChapter row.
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-orphan").SaveX(ctx)

	f := fake.New()
	d := download.New(client, f, hub, download.Config{
		PerProviderConcurrency: 1,

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Chapter must remain in wanted — no illegal wanted→failed transition.
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != "wanted" {
		t.Errorf("state: want wanted (no-provider chapter must not advance), got %s", got.State)
	}

	// A download.skip SSE event must have been emitted within a short window.
	var skipSeen bool
	timeout := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break drain
			}
			if ev.Type == "download.skip" {
				skipSeen = true
				break drain
			}
		case <-timeout:
			break drain
		}
	}
	if !skipSeen {
		t.Error("expected a download.skip SSE event for the no-provider chapter, got none")
	}
}

// TestDispatcher_BuildFetchRef_SuwayomiID verifies that buildFetchRef populates
// FetchRef.SuwayomiID from ProviderChapter.suwayomi_chapter_id, NOT from
// SeriesProvider.suwayomi_id. This is the M2 invariant: the dispatcher passes
// the per-chapter Suwayomi ID to the fetcher so it can call ChapterPages with
// the correct chapter identifier.
//
// The test uses a ref-capturing fetcher that records the FetchRef it receives,
// then asserts FetchRef.SuwayomiID == ProviderChapter.suwayomi_chapter_id.
func TestDispatcher_BuildFetchRef_SuwayomiID(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	const (
		seriesMangaID   = 77  // SeriesProvider.suwayomi_id (manga-level)
		chapterSuwayomi = 999 // ProviderChapter.suwayomi_chapter_id (chapter-level)
	)

	s := client.Series.Create().SetTitle("FetchRef Series").SetSlug("fetchref-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider("suwayomi").
		SetImportance(10).
		SetSuwayomiID(seriesMangaID). // manga-level ID — must NOT appear in FetchRef
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-fetchref").
		SetURL("https://suwayomi.test/ch/1").
		SetProviderIndex(0).
		SetSuwayomiChapterID(chapterSuwayomi). // chapter-level ID — must appear in FetchRef
		SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("ch-fetchref").SaveX(ctx)

	// Use a capturing fetcher to record the FetchRef the dispatcher constructs.
	cf := &fetchRefCapture{}
	d := download.New(client, cf, hub, download.Config{
		PerProviderConcurrency: 1,

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	cf.mu.Lock()
	gotSuwayomiID := cf.ref.SuwayomiID
	cf.mu.Unlock()

	if gotSuwayomiID != chapterSuwayomi {
		t.Errorf("FetchRef.SuwayomiID: got %d, want %d (chapter-level ID); "+
			"got the manga-level ID %d instead — buildFetchRef is using the wrong source",
			gotSuwayomiID, chapterSuwayomi, seriesMangaID)
	}
}

// TestDispatcher_RendersToSeriesCategory verifies that a downloaded chapter is
// rendered under the series' real category folder, not the hardcoded Other.
// Seeds a series with category=Manhwa and asserts the CBZ lands at
// <storage>/Manhwa/<Title>/... — the M3 fix for buildRenderMeta.
func TestDispatcher_RendersToSeriesCategory(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().
		SetTitle("Category Series").
		SetSlug("category-series").
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SaveX(ctx)
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
		PerProviderConcurrency: 1,

		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded, got %s", got.State)
	}

	// The CBZ must live under the Manhwa category folder, not Other.
	wantPath := filepath.Join(storageDir, "Manhwa", "Category Series", got.Filename)
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Errorf("CBZ not found under series category at %s: %v", wantPath, statErr)
	}
	otherPath := filepath.Join(storageDir, "Other", "Category Series", got.Filename)
	if _, statErr := os.Stat(otherPath); statErr == nil {
		t.Errorf("CBZ rendered to Other/ — category was hardcoded, not read from the series")
	}
}

// fetchRefCapture is a fetcher.ChapterFetcher that records the last FetchRef
// it received and returns a single-page success so the dispatcher can complete.
type fetchRefCapture struct {
	mu  sync.Mutex
	ref fetcher.FetchRef
}

// Fetch records ref and returns a minimal valid ChapterPages.
func (c *fetchRefCapture) Fetch(_ context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	c.mu.Lock()
	c.ref = ref
	c.mu.Unlock()
	return fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0xFF}, Ext: "jpg"}},
		PageCount: 1,
	}, nil
}

// Ensure uuid is used to keep the import — used in table-driven extensions.
var _ = uuid.Nil
