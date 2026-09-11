// Package chapter_test contains integration tests for the chapter ingest service.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package chapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

type failingProviderChapterCacheInvalidator struct {
	err error
}

func (f failingProviderChapterCacheInvalidator) BeginProviderChapterCacheInvalidation() chapter.ProviderChapterCacheInvalidation {
	return f
}

func (f failingProviderChapterCacheInvalidator) InvalidateProviderChapterCache(context.Context, uuid.UUID) error {
	return f.err
}

func (failingProviderChapterCacheInvalidator) Commit()         {}
func (failingProviderChapterCacheInvalidator) Rollback() error { return nil }

type firstBlockingProviderChapterCacheInvalidator struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (b *firstBlockingProviderChapterCacheInvalidator) BeginProviderChapterCacheInvalidation() chapter.ProviderChapterCacheInvalidation {
	return b
}

func (b *firstBlockingProviderChapterCacheInvalidator) InvalidateProviderChapterCache(context.Context, uuid.UUID) error {
	b.mu.Lock()
	b.calls++
	first := b.calls == 1
	b.mu.Unlock()
	if first {
		close(b.entered)
		<-b.release
	}
	return nil
}

func (*firstBlockingProviderChapterCacheInvalidator) Commit()         {}
func (*firstBlockingProviderChapterCacheInvalidator) Rollback() error { return nil }

func seedReconcileStaging(t *testing.T, root string, providerChapterID uuid.UUID) (string, string) {
	t.Helper()
	dir := filepath.Join(root, providerChapterID.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create staging dir: %v", err)
	}
	marker := filepath.Join(dir, "000001.jpg")
	if err := os.WriteFile(marker, []byte("staged"), 0o600); err != nil {
		t.Fatalf("create staged page: %v", err)
	}
	return dir, marker
}

func assertPreservedProviderChapterState(
	t *testing.T,
	providerChapter *ent.ProviderChapter,
	nextAttempt time.Time,
) {
	t.Helper()
	if len(providerChapter.PageLinks) != 0 {
		t.Errorf("page links = %+v, want cleared after address change", providerChapter.PageLinks)
	}
	if providerChapter.Attempts != 3 || providerChapter.LastError != "temporary failure" {
		t.Errorf("retry state = attempts %d, error %q; want preserved", providerChapter.Attempts, providerChapter.LastError)
	}
	if providerChapter.NextAttemptAt == nil || !providerChapter.NextAttemptAt.Equal(nextAttempt) {
		t.Errorf("next attempt = %v, want %v", providerChapter.NextAttemptAt, nextAttempt)
	}
}

func assertPreservedLogicalChapterState(t *testing.T, logicalChapter *ent.Chapter, providerID uuid.UUID) {
	t.Helper()
	if logicalChapter.State != entchapter.StateDownloaded || logicalChapter.Filename != "chapter-1.cbz" {
		t.Errorf("chapter state/filename = %s/%q, want downloaded/chapter-1.cbz", logicalChapter.State, logicalChapter.Filename)
	}
	if logicalChapter.SatisfiedByProviderID == nil || *logicalChapter.SatisfiedByProviderID != providerID || logicalChapter.SatisfiedImportance == nil || *logicalChapter.SatisfiedImportance != 20 {
		t.Errorf("chapter satisfaction changed: provider=%v importance=%v", logicalChapter.SatisfiedByProviderID, logicalChapter.SatisfiedImportance)
	}
}

// TestIngestDedupAcrossProviders verifies the core dedup invariant:
// ingesting the same chapter_key from two different SeriesProviders of one
// Series produces exactly ONE Chapter row and TWO ProviderChapter rows.
// The dedup is non-vacuous — it is the (series_id, chapter_key) unique index
// doing the work, not application-level filtering.
func TestIngestDedupAcrossProviders(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Dedup Test").SetSlug("dedup-test").SaveX(ctx)
	sp1 := client.SeriesProvider.Create().SetSeries(s).SetProvider("provider-a").SetImportance(1).SaveX(ctx)
	sp2 := client.SeriesProvider.Create().SetSeries(s).SetProvider("provider-b").SetImportance(2).SaveX(ctx)

	fc := chapter.FetchedChapter{
		Number:        ptr(12.0),
		Name:          "Chapter 12",
		URL:           "https://example.com/ch12",
		ProviderIndex: 0,
	}

	res1, err := chapter.IngestProviderChapters(ctx, client, sp1.ID, []chapter.FetchedChapter{fc})
	if err != nil {
		t.Fatalf("ingest sp1: %v", err)
	}
	if res1.NewChapters != 1 {
		t.Errorf("sp1 ingest: want 1 new chapter, got %d", res1.NewChapters)
	}
	if res1.NewProviderChapters != 1 {
		t.Errorf("sp1 ingest: want 1 new provider chapter, got %d", res1.NewProviderChapters)
	}

	res2, err := chapter.IngestProviderChapters(ctx, client, sp2.ID, []chapter.FetchedChapter{fc})
	if err != nil {
		t.Fatalf("ingest sp2: %v", err)
	}
	// The Chapter for key "12" already exists — must NOT create a second one.
	if res2.NewChapters != 0 {
		t.Errorf("sp2 ingest: want 0 new chapters (dedup), got %d", res2.NewChapters)
	}
	if res2.NewProviderChapters != 1 {
		t.Errorf("sp2 ingest: want 1 new provider chapter, got %d", res2.NewProviderChapters)
	}

	chapterCount := client.Chapter.Query().CountX(ctx)
	if chapterCount != 1 {
		t.Errorf("want exactly 1 chapter row, got %d", chapterCount)
	}

	pcCount := client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(sp1.ID)).
		CountX(ctx)
	pcCount += client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(sp2.ID)).
		CountX(ctx)
	if pcCount != 2 {
		t.Errorf("want exactly 2 provider chapter rows, got %d", pcCount)
	}
}

// TestIngestConcurrentRaceNoDuplicate verifies that a concurrent double-ingest of
// the same chapter_key produces one Chapter row and no error surfaced to callers.
// Both goroutines must complete without error even though only one can win the
// INSERT race; the loser must re-fetch the existing row instead of propagating
// the constraint error.
func TestIngestConcurrentRaceNoDuplicate(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Race Test").SetSlug("race-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("provider-race").SetImportance(1).SaveX(ctx)

	fc := chapter.FetchedChapter{
		Number:        ptr(5.0),
		ProviderIndex: 0,
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := range 2 {
		go func() {
			defer wg.Done()
			<-start
			_, err := chapter.IngestProviderChapters(ctx, client, sp.ID, []chapter.FetchedChapter{fc})
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d got error: %v", i, err)
		}
	}

	chapterCount := client.Chapter.Query().CountX(ctx)
	if chapterCount != 1 {
		t.Errorf("concurrent ingest: want 1 chapter row, got %d", chapterCount)
	}
}

// TestIngestKeyNormalizationDedup verifies that ingesting the same numeric chapter
// via two providers normalises to the same chapter_key and produces exactly one
// Chapter row. This exercises Task 1's NormalizeChapterKey via the ingest path.
func TestIngestKeyNormalizationDedup(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Norm Test").SetSlug("norm-test").SaveX(ctx)
	sp1 := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-norm-a").SetImportance(1).SaveX(ctx)
	sp2 := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-norm-b").SetImportance(2).SaveX(ctx)

	// Both 12.0 (float literal) and 12 (integer-like literal) normalise to
	// chapter_key "12" via NormalizeChapterKey — trailing zero is stripped.
	_, err := chapter.IngestProviderChapters(ctx, client, sp1.ID, []chapter.FetchedChapter{
		{Number: ptr(12.0), ProviderIndex: 0},
	})
	if err != nil {
		t.Fatalf("ingest 12.0: %v", err)
	}

	_, err = chapter.IngestProviderChapters(ctx, client, sp2.ID, []chapter.FetchedChapter{
		{Number: ptr(12), ProviderIndex: 0},
	})
	if err != nil {
		t.Fatalf("ingest 12 via sp2: %v", err)
	}

	chapterCount := client.Chapter.Query().CountX(ctx)
	if chapterCount != 1 {
		t.Errorf("normalisation dedup: want 1 chapter row, got %d", chapterCount)
	}

	// Confirm the normaliser produced key "12", not "12.0".
	ch := client.Chapter.Query().OnlyX(ctx)
	if ch.ChapterKey != "12" {
		t.Errorf("normalised chapter_key: want %q, got %q", "12", ch.ChapterKey)
	}
}

// TestSetStateIllegalTransitionRejected verifies that SetState rejects an illegal
// transition with an error and leaves the chapter state unchanged.
//
// downloading → wanted is the case used here: an IN-FLIGHT chapter can never be
// pulled back to the queue mid-fetch. It replaced downloaded → wanted, which
// became LEGAL with the owner re-download edge (QCAT-343) — the one edge into
// wanted that starts from a chapter WITH a file. Only the three owner-initiated
// sources (failed, permanently_failed, downloaded) may reach wanted.
func TestSetStateIllegalTransitionRejected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("State Test").SetSlug("state-test").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(entchapter.StateDownloading).
		SaveX(ctx)

	err := chapter.SetState(ctx, client, ch.ID, entchapter.StateWanted)
	if err == nil {
		t.Fatal("expected error for illegal transition downloading→wanted, got nil")
	}

	// State must be unchanged.
	refreshed := client.Chapter.GetX(ctx, ch.ID)
	if refreshed.State != entchapter.StateDownloading {
		t.Errorf("state changed unexpectedly: got %s", refreshed.State)
	}
}

// TestSetStateLegalTransitionSucceeds verifies that SetState accepts a legal
// transition (wanted → downloading) and persists the new state.
func TestSetStateLegalTransitionSucceeds(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("State OK Test").SetSlug("state-ok-test").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("2").
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	err := chapter.SetState(ctx, client, ch.ID, entchapter.StateDownloading)
	if err != nil {
		t.Fatalf("expected no error for wanted→downloading, got: %v", err)
	}

	refreshed := client.Chapter.GetX(ctx, ch.ID)
	if refreshed.State != entchapter.StateDownloading {
		t.Errorf("state not updated: want %s, got %s", entchapter.StateDownloading, refreshed.State)
	}
}

// TestSetStateOwnerRetryEdges verifies the two owner-retry edges added for the
// Downloads milestone: failed→wanted and permanently_failed→wanted both succeed
// and persist (the only legal paths back to wanted; the auto-dispatcher never
// uses them).
func TestSetStateOwnerRetryEdges(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Retry Edges").SetSlug("retry-edges").SaveX(ctx)

	cases := []struct {
		name string
		key  string
		from entchapter.State
	}{
		{"failed→wanted", "re-1", entchapter.StateFailed},
		{"permanently_failed→wanted", "re-2", entchapter.StatePermanentlyFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := client.Chapter.Create().
				SetSeries(s).
				SetChapterKey(tc.key).
				SetState(tc.from).
				SaveX(ctx)

			if err := chapter.SetState(ctx, client, ch.ID, entchapter.StateWanted); err != nil {
				t.Fatalf("expected %s to succeed, got: %v", tc.name, err)
			}
			refreshed := client.Chapter.GetX(ctx, ch.ID)
			if refreshed.State != entchapter.StateWanted {
				t.Errorf("state not reset: want wanted, got %s", refreshed.State)
			}
		})
	}
}

// TestIngestResultCounts verifies that IngestResult.NewChapters and
// IngestResult.NewProviderChapters count genuinely new rows only.
// A second ingest of the same list must report 0 new of each.
func TestIngestResultCounts(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Count Test").SetSlug("count-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-count").SetImportance(1).SaveX(ctx)

	chapters := []chapter.FetchedChapter{
		{Number: ptr(1.0), ProviderIndex: 0},
		{Number: ptr(2.0), ProviderIndex: 1},
		{Number: ptr(3.0), ProviderIndex: 2},
	}

	res, err := chapter.IngestProviderChapters(ctx, client, sp.ID, chapters)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if res.NewChapters != 3 {
		t.Errorf("first ingest: want NewChapters=3, got %d", res.NewChapters)
	}
	if res.NewProviderChapters != 3 {
		t.Errorf("first ingest: want NewProviderChapters=3, got %d", res.NewProviderChapters)
	}

	// Second ingest of the same list: no new rows.
	res2, err := chapter.IngestProviderChapters(ctx, client, sp.ID, chapters)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if res2.NewChapters != 0 {
		t.Errorf("second ingest: want NewChapters=0, got %d", res2.NewChapters)
	}
	if res2.NewProviderChapters != 0 {
		t.Errorf("second ingest: want NewProviderChapters=0, got %d", res2.NewProviderChapters)
	}
}

// TestSetStateChapterNotFound verifies that SetState returns a non-nil error
// when the given chapter ID does not exist.
func TestSetStateChapterNotFound(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	err := chapter.SetState(ctx, client, uuid.New(), entchapter.StateDownloading)
	if err == nil {
		t.Fatal("expected error for nonexistent chapter ID, got nil")
	}
}

// TestIngestProviderChaptersDBError verifies that IngestProviderChapters returns
// a non-nil error when the database is unavailable (cancelled context).
func TestIngestProviderChaptersDBError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// Create a real SeriesProvider before cancelling so the cancellation exercises
	// the chapter ingest path, not the SeriesProvider load path.
	s := client.Series.Create().SetTitle("Error Test").SetSlug("error-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-error").SetImportance(1).SaveX(ctx)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to simulate a dead DB connection

	chapters := []chapter.FetchedChapter{
		{Number: ptr(1.0), ProviderIndex: 0},
	}
	_, err := chapter.IngestProviderChapters(cancelCtx, client, sp.ID, chapters)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestIngestProviderChaptersChangedAddressInvalidatesOnlyResolverCache(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	nextAttempt := time.Date(2026, time.September, 12, 3, 4, 5, 0, time.UTC)

	series := client.Series.Create().SetTitle("Address Change").SetSlug("address-change").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SetImportance(20).SaveX(ctx)
	existingChapter := client.Chapter.Create().
		SetSeries(series).
		SetChapterKey("1").
		SetState(entchapter.StateDownloaded).
		SetFilename("chapter-1.cbz").
		SetSatisfiedByProviderID(provider.ID).
		SetSatisfiedImportance(20).
		SaveX(ctx)
	existingProviderChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/old/chapter-1").
		SetWebURL("https://old.example/chapter-1").
		SetAttempts(3).
		SetLastError("temporary failure").
		SetNextAttemptAt(nextAttempt).
		SetPageLinks([]fetcher.PageLink{{URL: "/page/1", ImageURL: "https://images.example/1"}}).
		SaveX(ctx)

	_, err := chapter.IngestProviderChapters(ctx, client, provider.ID, []chapter.FetchedChapter{{
		Number: ptr(1),
		Name:   "Chapter 1 refreshed",
		URL:    "/new/chapter-1",
		WebURL: "https://old.example/chapter-1",
	}})
	if err != nil {
		t.Fatalf("IngestProviderChapters: %v", err)
	}

	assertPreservedProviderChapterState(t, client.ProviderChapter.GetX(ctx, existingProviderChapter.ID), nextAttempt)
	assertPreservedLogicalChapterState(t, client.Chapter.GetX(ctx, existingChapter.ID), provider.ID)
}

func TestReconcileProviderChaptersAddressChangeInvalidatesStagingPair(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Staging Change").SetSlug("staging-change").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/old/chapter-1").
		SetWebURL("https://old.example/chapter-1").
		SetPageLinks([]fetcher.PageLink{{URL: "/page/1"}}).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)

	stagingRoot := t.TempDir()
	stagingDir, _ := seedReconcileStaging(t, stagingRoot, providerChapter.ID)

	_, err := chapter.ReconcileProviderChapters(ctx, client, provider.ID, []chapter.FetchedChapter{{
		Number: ptr(1),
		Name:   "Chapter 1",
		URL:    "/old/chapter-1",
		WebURL: "https://new.example/chapter-1",
	}}, download.NewProviderChapterCacheInvalidator(stagingRoot))
	if err != nil {
		t.Fatalf("ReconcileProviderChapters: %v", err)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir stat error = %v, want not exist", err)
	}
	if links := client.ProviderChapter.GetX(ctx, providerChapter.ID).PageLinks; len(links) != 0 {
		t.Errorf("page links = %+v, want cleared", links)
	}
}

func TestReconcileProviderChaptersInvalidationFailureLeavesCachePairAndAddressUnchanged(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Staging Failure").SetSlug("staging-failure").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/old/chapter-1").
		SetWebURL("https://old.example/chapter-1").
		SetPageLinks([]fetcher.PageLink{{URL: "/page/1"}}).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)

	wipeErr := errors.New("staging unavailable")
	_, err := chapter.ReconcileProviderChapters(ctx, client, provider.ID, []chapter.FetchedChapter{{
		Number: ptr(1),
		Name:   "Chapter 1 changed",
		URL:    "/new/chapter-1",
		WebURL: "https://new.example/chapter-1",
	}}, failingProviderChapterCacheInvalidator{err: wipeErr})
	if !errors.Is(err, wipeErr) {
		t.Fatalf("ReconcileProviderChapters error = %v, want wrapped %v", err, wipeErr)
	}

	got := client.ProviderChapter.GetX(ctx, providerChapter.ID)
	if got.URL != "/old/chapter-1" || got.WebURL != "https://old.example/chapter-1" {
		t.Errorf("address = %q/%q, want old address", got.URL, got.WebURL)
	}
	if len(got.PageLinks) != 1 {
		t.Errorf("page links = %+v, want retained", got.PageLinks)
	}
}

func TestReconcileProviderChaptersUnchangedAddressRetainsStagingPair(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Staging Stable").SetSlug("staging-stable").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/chapter-1").
		SetWebURL("https://example.test/chapter-1").
		SetPageLinks([]fetcher.PageLink{{URL: "/page/1"}}).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)

	stagingRoot := t.TempDir()
	_, marker := seedReconcileStaging(t, stagingRoot, providerChapter.ID)

	_, err := chapter.ReconcileProviderChapters(ctx, client, provider.ID, []chapter.FetchedChapter{{
		Number: ptr(1),
		Name:   "Chapter 1 renamed",
		URL:    "/chapter-1",
		WebURL: "https://example.test/chapter-1",
	}}, download.NewProviderChapterCacheInvalidator(stagingRoot))
	if err != nil {
		t.Fatalf("ReconcileProviderChapters: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("unchanged-address staging marker: %v", err)
	}
	if links := client.ProviderChapter.GetX(ctx, providerChapter.ID).PageLinks; len(links) != 1 {
		t.Errorf("page links = %+v, want retained", links)
	}
}

func TestReconcileProviderChaptersSerializesAddressComparisonThroughUpdate(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Concurrent Address").SetSlug("concurrent-address").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/original/chapter-1").
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)

	invalidator := &firstBlockingProviderChapterCacheInvalidator{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := chapter.ReconcileProviderChapters(ctx, client, provider.ID, []chapter.FetchedChapter{{
			Number: ptr(1),
			URL:    "/first/chapter-1",
		}}, invalidator)
		firstDone <- err
	}()
	<-invalidator.entered

	secondDone := make(chan error, 1)
	go func() {
		_, err := chapter.ReconcileProviderChapters(ctx, client, provider.ID, []chapter.FetchedChapter{{
			Number: ptr(1),
			URL:    "/second/chapter-1",
		}}, invalidator)
		secondDone <- err
	}()

	secondCompletedBeforeRelease := false
	select {
	case err := <-secondDone:
		secondCompletedBeforeRelease = true
		if err != nil {
			t.Errorf("second reconcile before release: %v", err)
		}
	case <-time.After(150 * time.Millisecond):
	}
	close(invalidator.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if !secondCompletedBeforeRelease {
		if err := <-secondDone; err != nil {
			t.Fatalf("second reconcile: %v", err)
		}
	}

	if secondCompletedBeforeRelease {
		t.Error("second reconcile completed while the first was paused after its address read")
	}
	if got := client.ProviderChapter.GetX(ctx, providerChapter.ID).URL; got != "/second/chapter-1" {
		t.Errorf("final provider chapter URL = %q, want second reconcile value", got)
	}
}

// TestAbsorbProviderChapterRace verifies absorbProviderChapterRace deterministically:
// given an existing ProviderChapter row, calling AbsorbProviderChapterRace with new
// values must re-fetch the row, update all mutable fields, and return nil error.
// This exercises the concurrent-insert loser path without relying on a real race.
func TestAbsorbProviderChapterRace(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Race Absorb Test").SetSlug("race-absorb-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-absorb").SetImportance(1).SaveX(ctx)

	const key = "7"

	// Seed an existing ProviderChapter row — this is the "winner" of the INSERT race.
	initialNum := ptr(7.0)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(key).
		SetNillableNumber(initialNum).
		SetName("Chapter 7 initial").
		SetURL("https://example.com/ch7-initial").
		SetProviderIndex(0).
		SaveX(ctx)

	// The "loser" goroutine calls AbsorbProviderChapterRace with updated values.
	newNum := ptr(7.0)
	newFC := chapter.FetchedChapter{
		Number:        newNum,
		Name:          "Chapter 7 updated",
		URL:           "https://example.com/ch7-updated",
		ProviderIndex: 99,
	}

	err := chapter.AbsorbProviderChapterRace(ctx, client, sp.ID, key, newFC)
	if err != nil {
		t.Fatalf("AbsorbProviderChapterRace returned unexpected error: %v", err)
	}

	// Verify the existing row was updated to the new values.
	rows := client.ProviderChapter.Query().AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 ProviderChapter row, got %d", len(rows))
	}
	got := rows[0]
	if got.Name != newFC.Name {
		t.Errorf("Name: want %q, got %q", newFC.Name, got.Name)
	}
	if got.URL != newFC.URL {
		t.Errorf("URL: want %q, got %q", newFC.URL, got.URL)
	}
	if got.ProviderIndex != newFC.ProviderIndex {
		t.Errorf("ProviderIndex: want %d, got %d", newFC.ProviderIndex, got.ProviderIndex)
	}
}

// TestAbsorbProviderChapterRaceVanishedRow exercises the "vanished row" branch of
// absorbProviderChapterRace: if the ProviderChapter row disappears between the
// constraint error and the re-fetch (possible since M6 introduced owner-initiated
// deletion via series.RemoveProvider), the function must return a wrapped error
// rather than panic.
//
// This is deterministically reachable without timing seams: calling
// AbsorbProviderChapterRace with no pre-existing row for the given
// (seriesProviderID, key) pair places us directly in the "winner vanished"
// state — no goroutine scheduling is required.
func TestAbsorbProviderChapterRaceVanishedRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Vanished Row Test").SetSlug("vanished-row-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-vanished").SetImportance(1).SaveX(ctx)

	// Call AbsorbProviderChapterRace for a key that has no pre-existing row.
	// This simulates: another ingest inserted the winner, then RemoveProvider
	// deleted that row before the losing call could re-fetch it.
	fc := chapter.FetchedChapter{
		Number:        ptr(3.0),
		Name:          "Chapter 3",
		URL:           "https://example.com/ch3",
		ProviderIndex: 0,
	}
	err := chapter.AbsorbProviderChapterRace(ctx, client, sp.ID, "3", fc)
	if err == nil {
		t.Fatal("expected an error when the ProviderChapter row does not exist, got nil")
	}
}
