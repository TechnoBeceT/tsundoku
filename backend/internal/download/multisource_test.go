// Package download_test — integration tests for the multi-source download
// engine: per-source retry exhaustion, immediate source fall-through, permanent
// failure only when every source is exhausted, and fastest-release.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// providerScopedFetcher fails for the named providers (per FetchRef.Provider) and
// succeeds for everyone else with a single deterministic page. It lets a test
// model "source X is down, source Y is up" precisely, which the fake fetcher's
// global fail modes cannot express. Safe for concurrent use (read-only after
// construction).
type providerScopedFetcher struct {
	failProviders map[string]bool
}

// Fetch returns an error when ref.Provider is in the fail set, else a minimal
// valid one-page ChapterPages.
func (f *providerScopedFetcher) Fetch(_ context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	if f.failProviders[ref.Provider] {
		return fetcher.ChapterPages{}, errors.New("provider " + ref.Provider + " is down")
	}
	return fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0xAB}, Ext: "jpg"}},
		PageCount: 1,
	}, nil
}

// twoSourceSeries seeds a series with a chapter (key "c1") offered by two
// sources: low importance = lowProvider, high importance = highProvider. It
// returns the client, the chapter, and the two SeriesProvider ids.
func twoSourceSeries(ctx context.Context, t *testing.T, lowProvider string, lowImp int, highProvider string, highImp int) (*ent.Client, *ent.Chapter) {
	t.Helper()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Multi Source").SetSlug("multi-source").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider(lowProvider).SetImportance(lowImp).SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider(highProvider).SetImportance(highImp).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey("c1").SetURL("https://low/c1").SetProviderIndex(0).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey("c1").SetURL("https://high/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)
	return client, ch
}

// pcByProvider loads the ProviderChapter belonging to the source with the given
// provider key (for asserting its per-source retry state).
func pcByProvider(ctx context.Context, t *testing.T, client *ent.Client, provider string) *ent.ProviderChapter {
	t.Helper()
	pcs, err := client.ProviderChapter.Query().WithSeriesProvider().All(ctx)
	if err != nil {
		t.Fatalf("load provider chapters: %v", err)
	}
	for _, pc := range pcs {
		if pc.Edges.SeriesProvider != nil && pc.Edges.SeriesProvider.Provider == provider {
			return pc
		}
	}
	t.Fatalf("no provider chapter for provider %q", provider)
	return nil
}

// TestProcess_SingleChapterFallThrough verifies the standalone Process entry
// point (not just the RunOnce batch path): it resolves its own retry budget +
// limiter and downloads one chapter, falling through a down source to a working one.
func TestProcess_SingleChapterFallThrough(t *testing.T) {
	ctx := context.Background()
	client, ch := twoSourceSeries(ctx, t, "low", 5, "high", 10)

	f := &providerScopedFetcher{failProviders: map[string]bool{"high": true}}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if err := d.Process(ctx, ch.ID); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded, got %s", got.State)
	}
	if got.SatisfiedImportance == nil || *got.SatisfiedImportance != 5 {
		t.Errorf("satisfied_importance: want 5 (fell through to low), got %v", got.SatisfiedImportance)
	}
}

// TestMultiSource_ImmediateFallThrough verifies that when the highest-importance
// source fails, the dispatcher falls through to the next source IN THE SAME CYCLE
// and downloads from it — reading is never blocked on a broken preferred source.
func TestMultiSource_ImmediateFallThrough(t *testing.T) {
	ctx := context.Background()
	client, ch := twoSourceSeries(ctx, t, "low", 5, "high", 10)

	f := &providerScopedFetcher{failProviders: map[string]bool{"high": true}}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded (fell through to the working source), got %s", got.State)
	}
	// The low source satisfied it; the high source recorded one failed attempt.
	if got.SatisfiedImportance == nil || *got.SatisfiedImportance != 5 {
		t.Errorf("satisfied_importance: want 5 (low source), got %v", got.SatisfiedImportance)
	}
	if high := pcByProvider(ctx, t, client, "high"); high.Attempts != 1 {
		t.Errorf("high source attempts: want 1 (one failed try this cycle), got %d", high.Attempts)
	}
	if low := pcByProvider(ctx, t, client, "low"); low.Attempts != 0 {
		t.Errorf("low source attempts: want 0 (it succeeded), got %d", low.Attempts)
	}
}

// TestMultiSource_FastestRelease verifies that when ONLY a lower-importance
// source currently has the chapter, it downloads from that source rather than
// waiting for a higher source that "might" list it later. Here the high source
// exists but does NOT offer this chapter key at all.
func TestMultiSource_FastestRelease(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Fastest").SetSlug("fastest").SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("high").SetImportance(10).SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetImportance(5).SaveX(ctx)
	// Only the LOW source offers chapter c1; the high source offers a different key.
	client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey("c1").SetURL("https://low/c1").SetProviderIndex(0).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey("c2").SetURL("https://high/c2").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	f := &providerScopedFetcher{}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded from the only available (lower) source, got %s", got.State)
	}
	if got.SatisfiedImportance == nil || *got.SatisfiedImportance != 5 {
		t.Errorf("satisfied_importance: want 5, got %v", got.SatisfiedImportance)
	}
}

// TestMultiSource_NoPermaFailWhileALiveSourceRemains verifies that a chapter is
// NEVER permanently failed while any source still has retry budget: the preferred
// source exhausts across cycles, then the surviving source downloads it.
func TestMultiSource_NoPermaFailWhileALiveSourceRemains(t *testing.T) {
	ctx := context.Background()
	// High source is down; low source will succeed. maxRetries=2, Backoff=0 so the
	// high source's cooldown is always already past and both sources are tried each
	// cycle. First cycle: high fails (attempts 1) → falls through → low succeeds.
	client, ch := twoSourceSeries(ctx, t, "low", 5, "high", 10)
	f := &providerScopedFetcher{failProviders: map[string]bool{"high": true}}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 2, Backoff: 0})

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state: want downloaded (a live source delivered it), got %s", got.State)
	}
	if got.State == entchapter.StatePermanentlyFailed {
		t.Fatal("must NOT permanently fail while a live source can deliver the chapter")
	}
}

// TestMultiSource_PermaFailOnlyWhenAllExhausted verifies that a chapter becomes
// permanently_failed exactly when EVERY source that offers it has exhausted its
// per-source retry budget — and not before.
func TestMultiSource_PermaFailOnlyWhenAllExhausted(t *testing.T) {
	ctx := context.Background()
	// Both sources are down. maxRetries=2, Backoff=0 (no cooldown between tries).
	client, ch := twoSourceSeries(ctx, t, "low", 5, "high", 10)
	f := &providerScopedFetcher{failProviders: map[string]bool{"low": true, "high": true}}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 2, Backoff: 0})

	// Cycle 1: each source tried once (attempts 1/1) — still failed, not permanent.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("cycle 1 RunOnce: %v", err)
	}
	after1 := client.Chapter.GetX(ctx, ch.ID)
	if after1.State != entchapter.StateFailed {
		t.Fatalf("after cycle 1: want failed (budget remains), got %s", after1.State)
	}
	if a := pcByProvider(ctx, t, client, "high").Attempts; a != 1 {
		t.Errorf("after cycle 1: high attempts want 1, got %d", a)
	}

	// Cycle 2: each source tried a second time (attempts 2/2 == maxRetries) → every
	// source exhausted → permanently_failed.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("cycle 2 RunOnce: %v", err)
	}
	after2 := client.Chapter.GetX(ctx, ch.ID)
	if after2.State != entchapter.StatePermanentlyFailed {
		t.Fatalf("after cycle 2: want permanently_failed (all sources exhausted), got %s", after2.State)
	}
	if a := pcByProvider(ctx, t, client, "low").Attempts; a != 2 {
		t.Errorf("after cycle 2: low attempts want 2, got %d", a)
	}
}

// TestMultiSource_BackoffGatesUntilNextAttempt verifies that a source failing
// with a positive backoff is NOT retried on the immediately-following cycle
// (its next_attempt_at is in the future). With a single down source and a long
// backoff, the chapter stays failed and the source keeps attempts=1 across a
// second cycle that finds it gated.
func TestMultiSource_BackoffGatesUntilNextAttempt(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Backoff").SetSlug("backoff").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("only").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").SetURL("https://only/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	f := &providerScopedFetcher{failProviders: map[string]bool{"only": true}}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 5, Backoff: time.Hour}) // long backoff, budget remaining

	// Cycle 1: the source fails once and is put on a 1h+ cooldown.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("cycle 1 RunOnce: %v", err)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateFailed {
		t.Fatalf("after cycle 1: want failed, got %s", st)
	}
	pc1 := providerChapterFor(ctx, t, client, "c1")
	if pc1.Attempts != 1 {
		t.Fatalf("after cycle 1: attempts want 1, got %d", pc1.Attempts)
	}
	if pc1.NextAttemptAt == nil || !pc1.NextAttemptAt.After(time.Now()) {
		t.Fatalf("after cycle 1: next_attempt_at must be in the future (cooldown), got %v", pc1.NextAttemptAt)
	}

	// Cycle 2 (immediately): the source is still on cooldown, so it is NOT a live
	// candidate — no new attempt is made. The chapter stays failed, attempts unchanged.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("cycle 2 RunOnce: %v", err)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateFailed {
		t.Fatalf("after cycle 2: want still failed (source gated by backoff), got %s", st)
	}
	if a := providerChapterFor(ctx, t, client, "c1").Attempts; a != 1 {
		t.Errorf("after cycle 2: attempts must stay 1 (no retry while gated), got %d", a)
	}
}

// TestMultiSource_WantedWithExhaustedSourceGoesPermaFail verifies the on-entry
// permanent-failure path: a wanted chapter whose only source is already exhausted
// (attempts >= maxRetries, e.g. carried in by reconcile/import or a manual edit)
// transitions straight to permanently_failed via the wanted→permanently_failed
// edge, without a fetch.
func TestMultiSource_WantedWithExhaustedSourceGoesPermaFail(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Exhausted Entry").SetSlug("exhausted-entry").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("only").SetImportance(10).SaveX(ctx)
	// The source is already exhausted (attempts == maxRetries) before this cycle.
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").SetURL("https://only/c1").SetProviderIndex(0).SetAttempts(3).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SetState(entchapter.StateWanted).SaveX(ctx)

	// A fetcher that would SUCCEED if ever called — proving no fetch happens.
	f := &providerScopedFetcher{}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: 0})

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StatePermanentlyFailed {
		t.Fatalf("state: want permanently_failed (only source already exhausted), got %s", got.State)
	}
	if got.LastError == "" {
		t.Error("last_error should explain the exhaustion")
	}
	// The source must not have been touched (no fetch, no bump).
	if a := providerChapterFor(ctx, t, client, "c1").Attempts; a != 3 {
		t.Errorf("attempts must be unchanged at 3 (no fetch attempted), got %d", a)
	}
}

// TestMultiSource_RetriedSourceRecoversAndDownloads verifies that after a source
// exhausts and the chapter permanently fails, an owner retry (which resets
// per-source attempts) lets the recovered source download it. This test drives
// the reset directly (the downloads service owns the retry endpoint) to prove the
// dispatcher treats a reset source as live again.
func TestMultiSource_RetriedSourceRecoversAndDownloads(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Recover").SetSlug("recover").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("only").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").SetURL("https://only/c1").SetProviderIndex(0).SetAttempts(3).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SetState(entchapter.StateWanted).SaveX(ctx)

	f := &providerScopedFetcher{} // would succeed
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: 0})

	// With the source exhausted, the chapter permanently fails.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce (exhausted): %v", err)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StatePermanentlyFailed {
		t.Fatalf("precondition: want permanently_failed, got %s", st)
	}

	// Reset per-source retry state + re-queue to wanted (what downloads.RetryChapter does).
	client.ProviderChapter.UpdateOneID(pc.ID).SetAttempts(0).ClearNextAttemptAt().ExecX(ctx)
	client.Chapter.UpdateOneID(ch.ID).SetState(entchapter.StateWanted).ExecX(ctx)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce (after reset): %v", err)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateDownloaded {
		t.Errorf("after reset: want downloaded (source recovered), got %s", st)
	}
}
