// Package job (white-box) test for upgradeAll's cancellation guard. It lives in
// package job — not job_test — because it drives the UNEXPORTED upgradeAll
// directly, which is the only faithful way to exercise the inner semaphore-window
// guard without adding a production injection seam (the Dispatcher is a concrete
// *download.Dispatcher, so a call-counting fake dispatcher is not available).
package job

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// blockingUpgradeFetcher blocks (honouring ctx) whenever the fetch is from
// blockProvider and succeeds instantly for every other provider. It lets the
// test park an in-flight upgrade fetch inside upgradeAll's bounded pool until the
// context is cancelled, deterministically reproducing the "closure already queued
// on the errgroup semaphore when the cancel lands" window the inner guard covers.
type blockingUpgradeFetcher struct {
	blockProvider string
	release       time.Duration
}

// Fetch blocks for blockProvider until release elapses or ctx is cancelled, and
// returns a minimal one-page success for any other provider.
func (f *blockingUpgradeFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	if ref.Provider == f.blockProvider {
		select {
		case <-time.After(f.release):
		case <-ctx.Done():
			return fetcher.ChapterPages{}, ctx.Err()
		}
	}
	return fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0x01}, Ext: "jpg"}},
		PageCount: 1,
	}, nil
}

// TestUpgradeAll_CancelSkipsQueuedUpgrade proves the inner cancellation guard:
// with DownloadConcurrency=1 and two flagged chapters, the first chapter's
// upgrade fetch blocks on the (slow) high provider while the loop parks inside
// the semaphore-blocking g.Go for the second chapter — the exact window the outer
// pre-g.Go check cannot cover. When the context is cancelled the blocked fetch
// releases the slot, the second closure unblocks, and the inner guard must return
// early WITHOUT calling Upgrade. Observable proof: exactly ONE chapter is
// processed (count == 1) and the OTHER remains upgrade_available — Upgrade's first
// action is a transition to upgrading, so an unguarded second run would have moved
// it off upgrade_available. upgradeAll must also return nil and return promptly.
func TestUpgradeAll_CancelSkipsQueuedUpgrade(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	// Fetches from prov-high block until cancel; prov-low (the initial download)
	// is instant, so both chapters download quickly before we flag any upgrade.
	f := &blockingUpgradeFetcher{blockProvider: "prov-high", release: 10 * time.Second}
	d := download.New(client, f, hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 1}, nil)
	r := NewRunner(d, client, hub, storage, settings.Static{})

	chIDs := seedTwoFlaggedUpgrades(ctx, t, r, d, client)

	// Run upgradeAll in the background; cancel once the first upgrade fetch is
	// parked and the second closure is blocked on the errgroup semaphore.
	upCtx, cancel := context.WithCancel(ctx)
	done := make(chan upgradeAllResult, 1)
	go func() {
		c, e := r.upgradeAll(upCtx, nil)
		done <- upgradeAllResult{c, e}
	}()

	time.Sleep(200 * time.Millisecond) // let the first fetch block and the loop park in g.Go
	cancel()

	assertUpgradeAllCancelled(t, done)

	// Exactly one chapter must still be upgrade_available: the one the inner guard
	// skipped before Upgrade could transition it to upgrading. (The processed one
	// left upgrade_available — its cancelled fetch routes through the upgrade
	// failure handler.)
	if stillFlagged := countUpgradeAvailable(ctx, client, chIDs); stillFlagged != 1 {
		t.Errorf("chapters still upgrade_available = %d, want 1 (guard skipped exactly one queued upgrade)", stillFlagged)
	}
}

// upgradeAllResult carries upgradeAll's return values across the goroutine
// boundary so the test can assert on them under a timeout.
type upgradeAllResult struct {
	count int
	err   error
}

// seedTwoFlaggedUpgrades creates two independent downloaded chapters (each from a
// fast prov-low), attaches a strictly-higher prov-high whose fetch blocks, and
// flags both upgrade_available via DetectUpgrades. It returns the two chapter ids.
func seedTwoFlaggedUpgrades(ctx context.Context, t *testing.T, r *Runner, d *download.Dispatcher, client *ent.Client) []uuid.UUID {
	t.Helper()
	keys := []string{"cancel-upg-1", "cancel-upg-2"}
	series := make([]*ent.Series, len(keys))
	chIDs := make([]uuid.UUID, len(keys))
	for i, key := range keys {
		s := client.Series.Create().
			SetTitle(fmt.Sprintf("Cancel Upg %d", i)).
			SetSlug(fmt.Sprintf("cancel-upg-%d", i)).
			SaveX(ctx)
		spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
		client.ProviderChapter.Create().
			SetSeriesProviderID(spLow.ID).SetChapterKey(key).
			SetURL("https://low/" + key).SetProviderIndex(0).SaveX(ctx)
		ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SaveX(ctx)
		series[i] = s
		chIDs[i] = ch.ID
	}

	// Download both chapters from the fast low provider (no high provider yet, so
	// this cycle's own upgrade pass is a no-op).
	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatalf("initial RunDownloadCycle: %v", err)
	}
	for _, id := range chIDs {
		if st := client.Chapter.GetX(ctx, id).State; st != entchapter.StateDownloaded {
			t.Fatalf("chapter %s should be downloaded after first cycle, got %s", id, st)
		}
	}

	// Add a strictly-higher provider (whose fetch blocks) to each series and flag
	// both chapters upgrade_available.
	for i, key := range keys {
		spHigh := client.SeriesProvider.Create().SetSeries(series[i]).SetProvider("prov-high").SetImportance(10).SaveX(ctx)
		client.ProviderChapter.Create().
			SetSeriesProviderID(spHigh.ID).SetChapterKey(key).
			SetURL("https://high/" + key).SetProviderIndex(0).SaveX(ctx)
	}
	flagged, err := d.DetectUpgrades(ctx, d.MaxRetries(ctx))
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if flagged != len(keys) {
		t.Fatalf("DetectUpgrades flagged %d, want %d", flagged, len(keys))
	}
	return chIDs
}

// assertUpgradeAllCancelled waits for upgradeAll to return and asserts it did so
// promptly, without error, and processed exactly one chapter (the other skipped by
// the cancel guard).
func assertUpgradeAllCancelled(t *testing.T, done <-chan upgradeAllResult) {
	t.Helper()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("upgradeAll returned error after cancel, want nil: %v", got.err)
		}
		if got.count != 1 {
			t.Errorf("upgradeAll count = %d, want 1 (first processed, second skipped by cancel guard)", got.count)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upgradeAll did not return within 5s after cancel — cancellation guard did not fire")
	}
}

// countUpgradeAvailable returns how many of the given chapters are currently in
// state=upgrade_available.
func countUpgradeAvailable(ctx context.Context, client *ent.Client, ids []uuid.UUID) int {
	n := 0
	for _, id := range ids {
		if client.Chapter.GetX(ctx, id).State == entchapter.StateUpgradeAvailable {
			n++
		}
	}
	return n
}
