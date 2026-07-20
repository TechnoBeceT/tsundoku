// Package download_test — the CLASSIFIED retry-accounting proofs (owner-ratified:
// a fetch's error class drives TWO separate things — the per-(chapter,source)
// counter AND the circuit-breaker):
//   - A CHAPTER-SPECIFIC failure (not_found / no_pages / parse / broken page / no
//     live source) BUMPS the per-source budget (attempts++, terminal at max) and does
//     NOT trip the breaker — the source stays available for its other chapters
//     (TestFetchFailure_ChapterSpecific_BumpsBudget_NoBreaker).
//   - A SOURCE-WIDE/ban failure (rate_limit / captcha / timeout / network /
//     server_error / unknown) does NOT bump the budget (only cools the chapter down)
//     and DOES trip the breaker — so a ban never drains the queue and the whole
//     source is paused (TestFetchFailure_SourceWide_CooldownsNoBump_TripsBreaker).
//   - A source whose breaker is TRIPPED is excluded from candidacy (filterGated), so
//     its chapters stay wanted and burn NO attempts while it is excluded
//     (TestBreakerTripped_HoldsTheLine_NoAttemptsBurned). The per-chapter max bites a
//     chapter the source genuinely can't serve (chapter-specifically) while it is UP
//     (TestSourceAvailable_TerminatesAtMax).
//   - A local render/persist fault (finishDownload) charges NO source at all (⑥).
//
// Requires Docker (via testcontainers).
package download_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// singleSourceChapter seeds a series with one source ("mangadex", importance 10)
// offering one wanted chapter ("c1"), returning the chapter and its ProviderChapter.
func singleSourceChapter(ctx context.Context, t *testing.T, client *ent.Client) (*ent.Chapter, *ent.ProviderChapter) {
	t.Helper()
	s := client.Series.Create().SetTitle("Budget").SetSlug("budget").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("c1").SetURL("https://x/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)
	return ch, pc
}

// classifiedSettings satisfies BOTH download.RetrySettings and sourcegate.Thresholds
// with a failure threshold of 1, so a SINGLE source-wide failure trips the breaker
// (letting the tests assert the breaker axis directly).
func classifiedSettings() settings.Static {
	return settings.Static{
		Retries: 3, Backoff: 30 * time.Minute, DownloadConc: 1,
		SourcesFailureThresh: 1, SourcesCooldownIv: time.Hour, SourcesMinDelay: 0,
	}
}

// TestFetchFailure_ChapterSpecific_BumpsBudget_NoBreaker proves the chapter-specific
// half of the classified model: a broken-page / not_found / no_pages / parse failure
// BUMPS the per-source budget (attempts 0→1, cooldown set) but NEVER trips the
// source's circuit-breaker — the source stays available for its other chapters.
func TestFetchFailure_ChapterSpecific_BumpsBudget_NoBreaker(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{"not_found", "chapter not found"},
		{"no_pages", "chapter has no pages"},
		{"parse", "malformed response body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			_, pc := singleSourceChapter(ctx, t, client) // provider "mangadex" ⇒ breaker key "mangadex"

			rs := classifiedSettings()
			gate := sourcegate.NewService(client, rs)
			f := fake.New(fake.WithError(errors.New(tc.errMsg)))
			d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			got := client.ProviderChapter.GetX(ctx, pc.ID)
			if got.Attempts != 1 {
				t.Errorf("attempts = %d, want 1 (a chapter-specific failure charges the budget)", got.Attempts)
			}
			if got.NextAttemptAt == nil {
				t.Error("next_attempt_at should be set (the flat backoff cooldown)")
			}
			if !gate.IsAvailable(ctx, "mangadex", time.Now()) {
				t.Error("breaker tripped on a chapter-specific failure — the source must stay available for its other chapters")
			}
		})
	}
}

// TestFetchFailure_SourceWide_CooldownsNoBump_TripsBreaker proves the source-wide
// half: a rate_limit / captcha / timeout / network / server_error / unknown failure
// does NOT spend the chapter's budget (attempts stays 0 — a ban never drains the
// queue) but DOES trip the source's circuit-breaker (paused at threshold 1).
func TestFetchFailure_SourceWide_CooldownsNoBump_TripsBreaker(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{"rate_limit", "429 too many requests"},
		{"captcha", "cloudflare challenge detected"},
		{"timeout", "request timed out: deadline exceeded"},
		{"network", "connection reset by peer"},
		{"server_error", "502 bad gateway"},
		{"unknown", "something inexplicable happened"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			_, pc := singleSourceChapter(ctx, t, client)

			rs := classifiedSettings()
			gate := sourcegate.NewService(client, rs)
			f := fake.New(fake.WithError(errors.New(tc.errMsg)))
			d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			got := client.ProviderChapter.GetX(ctx, pc.ID)
			if got.Attempts != 0 {
				t.Errorf("attempts = %d, want 0 (a source-wide/ban failure must not spend the budget)", got.Attempts)
			}
			if got.NextAttemptAt == nil {
				t.Error("next_attempt_at should be set (the source-wide cooldown still defers this chapter)")
			}
			if gate.IsAvailable(ctx, "mangadex", time.Now()) {
				t.Error("breaker did NOT trip on a source-wide failure — the whole source must be paused")
			}
		})
	}
}

// TestBreakerTripped_HoldsTheLine_NoAttemptsBurned is the NEW drain-prevention
// proof: the circuit-breaker — NOT the retry classification — is what stops an
// anti-bot ban from draining the queue. A source whose breaker is already TRIPPED
// (cooldown_until in the future) is EXCLUDED from candidacy (filterGated), so
// across many cycles its chapter is never fetched, stays wanted, and its per-source
// attempts are NEVER incremented — the breaker holds the line while the source is
// down. (Contrast TestSourceAvailable_TerminatesAtMax, where an AVAILABLE source's
// chapter does terminate at max_retries.)
func TestBreakerTripped_HoldsTheLine_NoAttemptsBurned(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch, pc := singleSourceChapter(ctx, t, client) // provider "mangadex" ⇒ breaker key "mangadex"

	// Pre-trip the breaker: cooldown_until one hour out (mirrors the E1
	// TestGate_PreTrippedSourceExcludedFromCandidacy_StaysWanted seeding).
	client.SourceCircuitState.Create().
		SetSourceKey("mangadex").
		SetConsecutiveFailures(5).
		SetCooldownUntil(time.Now().Add(time.Hour)).
		SetLastError("simulated prior block").
		SaveX(ctx)

	// A tiny budget with zero backoff: WITHOUT the breaker, this would exhaust to
	// permanently_failed in two cycles. The tripped breaker must exclude the source
	// so no attempt is ever made and the budget is never spent.
	rs := settings.Static{Retries: 2, Backoff: 0, DownloadConc: 1, SourcesFailureThresh: 5, SourcesCooldownIv: time.Hour}
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{err: errors.New("cloudflare challenge")}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	for cycle := 1; cycle <= 4; cycle++ {
		if _, err := d.RunOnce(ctx); err != nil {
			t.Fatalf("cycle %d RunOnce: %v", cycle, err)
		}
		if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateWanted {
			t.Fatalf("cycle %d: state = %s, want wanted (excluded by the tripped breaker)", cycle, st)
		}
	}
	if got := f.calls.Load(); got != 0 {
		t.Errorf("fetch calls = %d, want 0 (a tripped source is never attempted)", got)
	}
	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 0 {
		t.Errorf("attempts = %d, want 0 (the breaker holds the line — no attempt, no charge)", a)
	}
}

// TestSourceAvailable_TerminatesAtMax proves the chapter-specific terminal path:
// while a source is available (its breaker never trips — a chapter-specific failure
// does not record a breaker failure at all), a chapter it repeatedly fails
// CHAPTER-SPECIFICALLY DOES exhaust — its per-source attempts climb by one each
// cycle and it reaches permanently_failed exactly at max_retries.
func TestSourceAvailable_TerminatesAtMax(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch, pc := singleSourceChapter(ctx, t, client)

	const maxRetries = 3
	// A chapter-specific error ("not found") never records a breaker failure, so the
	// source stays AVAILABLE every cycle regardless of threshold and the per-chapter
	// max is the sole terminal driver.
	rs := settings.Static{Retries: maxRetries, Backoff: 0, DownloadConc: 1, SourcesFailureThresh: 100, SourcesCooldownIv: time.Hour}
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{err: errors.New("chapter not found")}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	var final entchapter.State
	for cycle := 1; cycle <= maxRetries+2; cycle++ {
		if _, err := d.RunOnce(ctx); err != nil {
			t.Fatalf("cycle %d RunOnce: %v", cycle, err)
		}
		final = client.Chapter.GetX(ctx, ch.ID).State
		if final == entchapter.StatePermanentlyFailed {
			break
		}
	}
	if final != entchapter.StatePermanentlyFailed {
		t.Fatalf("state = %s, want permanently_failed (an available source's chapter must terminate at max)", final)
	}
	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != maxRetries {
		t.Errorf("attempts = %d, want %d (every retry counts toward the max)", a, maxRetries)
	}
	if got := f.calls.Load(); got != int64(maxRetries) {
		t.Errorf("fetch calls = %d, want %d (each retry actually attempts the available source)", got, maxRetries)
	}
}

// TestDiskOriginProvider_ExhaustsNotLoops proves a wanted chapter whose ONLY
// candidate is a disk-origin provider (non-numeric, no live source) exhausts: the
// real Fetcher fails with ErrNotLiveSource, which — like every fetch failure —
// charges the budget, so the chapter reaches permanently_failed instead of looping
// forever.
func TestDiskOriginProvider_ExhaustsNotLoops(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Disk").SetSlug("disk").SaveX(ctx)
	// Non-numeric provider name = a disk-origin (suwayomi_id==0) source.
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("Weeb Central").SetImportance(1).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	// The REAL fetcher: a non-numeric provider fails with ErrNotLiveSource before any
	// client call, so the fake engine client is never actually invoked.
	d := download.New(client, sourceengine.NewFetcher(enginefake.New(), mustTempDir(t)), sse.NewHub(),
		download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 1, Backoff: 0, DownloadConc: 1}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 1 {
		t.Errorf("attempts = %d, want 1 (a disk-origin candidate charges the budget)", a)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StatePermanentlyFailed {
		t.Errorf("state = %s, want permanently_failed (the phantom source must exhaust, not loop forever)", st)
	}
}

// TestPermanentlyFailed_CleansChapterStaging proves the terminal-fail staging
// cleanup (FIX 3): when a chapter reaches permanently_failed via a chapter-specific
// error, the dispatcher removes its per-provider staging dir so partial pages that
// will never be resumed don't leak until the next startup GC.
func TestPermanentlyFailed_CleansChapterStaging(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := mustTempDir(t)

	_, pc := singleSourceChapter(ctx, t, client)

	// Simulate a partial download left on disk for this provider chapter.
	stagingDir := filepath.Join(stagingRoot, pc.ID.String())
	if err := os.MkdirAll(stagingDir, 0o750); err != nil {
		t.Fatalf("seed staging dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "0000.jpg"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("seed staged page: %v", err)
	}

	// A "parse"-classified chapter-specific error with a 1-attempt budget: one pass
	// bumps attempts 0→1 ⇒ exhausted ⇒ permanently_failed ⇒ terminal staging cleanup.
	f := fake.New(fake.WithError(errors.New("malformed response body")))
	d := download.New(client, f, sse.NewHub(),
		download.Config{Storage: mustTempDir(t), StagingRoot: stagingRoot},
		settings.Static{Retries: 1, Backoff: 0, DownloadConc: 1}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir %s survived permanently_failed (want cleaned); stat err = %v", stagingDir, err)
	}
}

// TestRenderFault_DoesNotChargeSource proves ⑥: a SUCCESSFUL fetch whose
// finishDownload fails (a local disk fault) charges NO source — the retry budget is
// untouched, so a persistent infra fault can never drain the library.
func TestRenderFault_DoesNotChargeSource(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch, pc := singleSourceChapter(ctx, t, client)

	// Point Storage at a regular FILE so RenderChapter's MkdirAll fails: the fetch
	// succeeds, but finishDownload (render) errors — a local fault, not the source's.
	storageFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(storageFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed storage file: %v", err)
	}

	f := fake.New() // succeeds with default pages
	d := download.New(client, f, sse.NewHub(),
		download.Config{Storage: storageFile},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 1}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 0 {
		t.Errorf("attempts = %d, want 0 (a render/persist fault must not charge the source)", a)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateFailed {
		t.Errorf("state = %s, want failed (retryable — the infra fault did not exhaust the source)", st)
	}
}

// TestFetchFailure_TransientImage_ChapterSpecific_NoBreaker proves the per-image
// retry + chapter-specific classification end-to-end through the REAL staging
// Fetcher: a persistent transient IMAGE failure (a 502 on every page byte fetch) is
// retried in stagePages, then surfaces wrapped in ErrImageFetch — so it BUMPS the
// per-source budget (attempts 0→1) but NEVER trips the source breaker (threshold 1),
// even though a bare 502 is a source-wide errorclass category. One flaky page can
// therefore never pause an otherwise-healthy source.
func TestFetchFailure_TransientImage_ChapterSpecific_NoBreaker(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Flaky").SetSlug("flaky").SaveX(ctx)
	// Numeric provider ⇒ the real Fetcher parses it as a live source id; breaker key "7".
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	rs := classifiedSettings() // SourcesFailureThresh 1 ⇒ any source-wide failure would trip the breaker
	gate := sourcegate.NewService(client, rs)
	engineClient := enginefake.New(
		enginefake.WithPages(7, "/ch/c1", []sourceengine.Page{{Index: 0, URL: "u0"}}),
		enginefake.WithError("Image", errors.New("502 bad gateway")), // transient, on every attempt
	)
	d := download.New(client, sourceengine.NewFetcher(engineClient, mustTempDir(t)), sse.NewHub(),
		download.Config{Storage: mustTempDir(t)}, rs, gate)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 1 {
		t.Errorf("attempts = %d, want 1 (a surviving image failure is chapter-specific → charges the budget)", a)
	}
	if !gate.IsAvailable(ctx, "7", time.Now()) {
		t.Error("breaker tripped on a per-image failure — one flaky page must never pause a healthy source")
	}
	if n := engineClient.CallCount("Image"); n != 4 {
		t.Errorf("Image called %d times, want 4 (1 initial + 3 retries against the flaky source)", n)
	}
}

// TestFetchFailure_PagesResolution_SourceWide_TripsBreaker proves the ban-detection
// carve-out survives the change: a page-RESOLUTION (Client.Pages) failure — an
// EARLIER session stage where a real ban blocks the whole source — stays SOURCE-WIDE
// through the real Fetcher (it is NEVER wrapped in ErrImageFetch), so it does not
// spend the chapter budget and DOES trip the source breaker (threshold 1).
func TestFetchFailure_PagesResolution_SourceWide_TripsBreaker(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Banned").SetSlug("banned").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	rs := classifiedSettings()
	gate := sourcegate.NewService(client, rs)
	engineClient := enginefake.New(enginefake.WithError("Pages", errors.New("502 bad gateway")))
	d := download.New(client, sourceengine.NewFetcher(engineClient, mustTempDir(t)), sse.NewHub(),
		download.Config{Storage: mustTempDir(t)}, rs, gate)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 0 {
		t.Errorf("attempts = %d, want 0 (a source-wide page-resolution failure must not spend the budget)", a)
	}
	if gate.IsAvailable(ctx, "7", time.Now()) {
		t.Error("breaker did NOT trip on a page-resolution failure — a real ban at the session stage must pause the source")
	}
	if n := engineClient.CallCount("Image"); n != 0 {
		t.Errorf("Image called %d times, want 0 (a Pages failure fails before any image fetch)", n)
	}
}

// TestStagingDir_DeletedAfterCBZCompleted proves the byte cache self-cleans: with
// the real staging Fetcher, a successful download assembles the CBZ and then DELETES
// the chapter's staging directory (bytes are held only for in-progress chapters).
func TestStagingDir_DeletedAfterCBZCompleted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := mustTempDir(t)
	stagingRoot := mustTempDir(t)

	s := client.Series.Create().SetTitle("Staging").SetSlug("staging").SaveX(ctx)
	// Numeric provider so the engine Fetcher parses it as a live source id.
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	jpg := encodeTestJPEG(t)
	engineClient := enginefake.New(
		enginefake.WithPages(7, "/ch/c1", []sourceengine.Page{{Index: 0, URL: "u0"}, {Index: 1, URL: "u1"}}),
		enginefake.WithImage(7, "u0", jpg, "image/jpeg"),
		enginefake.WithImage(7, "u1", jpg, "image/jpeg"),
	)
	d := download.New(client, sourceengine.NewFetcher(engineClient, stagingRoot), sse.NewHub(),
		download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 1}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateDownloaded {
		t.Fatalf("state = %s, want downloaded", st)
	}
	stagingDir := filepath.Join(stagingRoot, pc.ID.String())
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir %s still exists after completion (want deleted); stat err = %v", stagingDir, err)
	}
	// The stored page links were written through for a future re-download / resume.
	if got := client.ProviderChapter.GetX(ctx, pc.ID); len(got.PageLinks) != 2 {
		t.Errorf("page_links persisted = %d, want 2 (write-through on first resolve)", len(got.PageLinks))
	}
}
