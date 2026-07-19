// Package download_test — the ban-fix retry-accounting proofs (GAP-099).
//
// These pin the asymmetry that stops an anti-bot ban from draining the queue:
//   - A ban / source-down fetch failure (rate_limit, captcha, timeout, network,
//     server_error, unknown, AND a broken page — an HTML challenge served as a
//     200 image) only COOLS THE SOURCE DOWN — the per-source retry budget is
//     untouched, so the chapter waits for the cooldown and NEVER reaches
//     permanently_failed. (Broken-page cooldown is proven in brokenimage_test.go.)
//   - A chapter-specific fetch failure (not_found, no_pages, parse, and a
//     disk-origin "not a live source") BUMPS the budget as before, so a source that
//     genuinely can't serve a chapter still exhausts and the chapter falls through /
//     permanently fails.
//   - A local render/persist fault (finishDownload) charges NO source at all.
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

// TestFetchFailure_ClassificationDrivesBudget proves the classification split: a
// ban/source-down error leaves attempts at 0 (cooldown only), while a
// chapter-specific error increments attempts (bump).
func TestFetchFailure_ClassificationDrivesBudget(t *testing.T) {
	cases := []struct {
		name        string
		errMsg      string
		wantAttempt int
	}{
		{"rate_limit cools down", "429 too many requests", 0},
		{"captcha cools down", "cloudflare challenge detected", 0},
		{"timeout cools down", "request timed out: deadline exceeded", 0},
		{"network cools down", "connection reset by peer", 0},
		{"server_error cools down", "502 bad gateway", 0},
		{"unknown cools down", "something inexplicable happened", 0},
		{"not_found bumps", "chapter not found", 1},
		{"no_pages bumps", "chapter has no pages", 1},
		{"parse bumps", "malformed response body", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			_, pc := singleSourceChapter(ctx, t, client)

			f := fake.New(fake.WithError(errors.New(tc.errMsg)))
			d := download.New(client, f, sse.NewHub(),
				download.Config{Storage: mustTempDir(t)},
				settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 1}, nil)

			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			got := client.ProviderChapter.GetX(ctx, pc.ID)
			if got.Attempts != tc.wantAttempt {
				t.Errorf("attempts = %d, want %d", got.Attempts, tc.wantAttempt)
			}
			// Either way the source is put on a cooldown so it is not re-hit immediately.
			if got.NextAttemptAt == nil {
				t.Error("next_attempt_at should be set (both bump and cooldown set it)")
			}
		})
	}
}

// TestBanClass_NeverDrainsQueue is the queue-drain regression proof: a source that
// fails a whole backlog with a ban-class error across many cycles NEVER pushes its
// chapters to permanently_failed — they stay retryable (failed), retry budget
// untouched — so a temporary ban can never wipe the library.
func TestBanClass_NeverDrainsQueue(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch, pc := singleSourceChapter(ctx, t, client)

	// A ban-class failure (captcha) with a tiny budget and zero backoff: under the
	// OLD "any failure bumps" behaviour this would exhaust to permanently_failed in
	// two cycles; under the fix it must stay failed forever.
	f := fake.New(fake.WithError(errors.New("cloudflare challenge")))
	d := download.New(client, f, sse.NewHub(),
		download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 2, Backoff: 0, DownloadConc: 1}, nil)

	for cycle := 1; cycle <= 4; cycle++ {
		if _, err := d.RunOnce(ctx); err != nil {
			t.Fatalf("cycle %d RunOnce: %v", cycle, err)
		}
		got := client.Chapter.GetX(ctx, ch.ID)
		if got.State == entchapter.StatePermanentlyFailed {
			t.Fatalf("cycle %d: chapter drained to permanently_failed under a ban (must never happen)", cycle)
		}
		if got.State != entchapter.StateFailed {
			t.Fatalf("cycle %d: state = %s, want failed (retryable, waiting on cooldown)", cycle, got.State)
		}
	}
	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 0 {
		t.Errorf("attempts = %d, want 0 (a ban never spends the retry budget)", a)
	}
}

// TestDiskOriginProvider_ExhaustsNotLoops proves the disk-origin carve-out
// (FIX 5): a wanted chapter whose ONLY candidate is a disk-origin provider
// (non-numeric, no live source) is CHAPTER-SPECIFIC — the real Fetcher fails with
// ErrNotLiveSource, which charges the budget so the chapter exhausts to
// permanently_failed instead of retrying forever on a ban-class cooldown.
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
