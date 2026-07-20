// Package download_test — the retry-accounting proofs for the Kaizoku-style
// "count every retry, terminal at max" model (owner-ratified, replacing the old
// ban-vs-chapter classification of GAP-099):
//   - EVERY fetch failure BUMPS the per-source retry budget, regardless of class
//     (rate_limit / captcha / timeout / network / server_error / unknown / broken
//     page / not_found / no_pages / parse). A chapter goes permanently_failed once
//     every source offering it has spent its whole max_retries budget.
//   - The drain-prevention against an anti-bot ban is the CIRCUIT-BREAKER, not the
//     retry classification: a source whose breaker is TRIPPED is excluded from
//     candidacy (filterGated), so its chapters stay wanted and burn NO attempts
//     while it is excluded (TestBreakerTripped_HoldsTheLine_NoAttemptsBurned). The
//     per-chapter max mainly bites a chapter the source genuinely can't serve while
//     it is UP (TestSourceAvailable_TerminatesAtMax).
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

// TestFetchFailure_EveryClassBumpsBudget proves the Kaizoku-style model: EVERY
// fetch-failure class — ban/source-down AND chapter-specific alike — bumps the
// per-source retry budget (attempts 0→1) and sets a cooldown. There is no longer a
// class that leaves attempts untouched (that was the retired classification).
func TestFetchFailure_EveryClassBumpsBudget(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{"rate_limit bumps", "429 too many requests"},
		{"captcha bumps", "cloudflare challenge detected"},
		{"timeout bumps", "request timed out: deadline exceeded"},
		{"network bumps", "connection reset by peer"},
		{"server_error bumps", "502 bad gateway"},
		{"unknown bumps", "something inexplicable happened"},
		{"not_found bumps", "chapter not found"},
		{"no_pages bumps", "chapter has no pages"},
		{"parse bumps", "malformed response body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			_, pc := singleSourceChapter(ctx, t, client)

			f := fake.New(fake.WithError(errors.New(tc.errMsg)))
			d := download.New(client, f, sse.NewHub(),
				download.Config{Storage: mustTempDir(t)},
				settings.Static{Retries: 3, Backoff: 30 * time.Minute, DownloadConc: 1}, nil)

			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			got := client.ProviderChapter.GetX(ctx, pc.ID)
			if got.Attempts != 1 {
				t.Errorf("attempts = %d, want 1 (every failure class charges the budget)", got.Attempts)
			}
			// The source is put on a cooldown so it is not re-hit immediately.
			if got.NextAttemptAt == nil {
				t.Error("next_attempt_at should be set (the flat backoff cooldown)")
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

// TestSourceAvailable_TerminatesAtMax proves the other half of the model: while a
// source's breaker is NOT tripped (it is available), a chapter it repeatedly fails
// DOES exhaust — its per-source attempts climb by one each cycle and it reaches
// permanently_failed exactly at max_retries. The failure threshold is set high so
// the breaker never trips on this tiny backlog (per the tunables' documented
// small-backlog carve-out), isolating the per-chapter max as the terminal driver.
func TestSourceAvailable_TerminatesAtMax(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch, pc := singleSourceChapter(ctx, t, client)

	const maxRetries = 3
	// High SourcesFailureThresh ⇒ the breaker never trips over this single-chapter
	// run, so the source stays AVAILABLE every cycle and the per-chapter max bites.
	rs := settings.Static{Retries: maxRetries, Backoff: 0, DownloadConc: 1, SourcesFailureThresh: 100, SourcesCooldownIv: time.Hour}
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{err: errors.New("cloudflare challenge")}
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
