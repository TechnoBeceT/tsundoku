// Package download_test — write-failure accounting for the bounded-pass drain
// loop. Proves RunOnce's returned count is FORWARD PROGRESS (successful
// wanted/failed→downloading claim), not mere selection, so a writes-fail/
// reads-succeed DB fault yields dispatched==0 and cannot livelock the drain loop
// in job.Runner.RunDownloadCycle. Tests require Docker (via testcontainers).
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
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// installClaimWriteFailure hooks the Chapter so that every wanted/failed→
// downloading claim WRITE fails while all reads (queries) still succeed —
// modelling a writes-fail/reads-succeed DB fault (disk-full storage volume, or a
// read-only replica after failover). ent hooks fire ONLY on mutations, so the
// read inside chapter.SetState (client.Chapter.Get) succeeds and only the
// UpdateOneID(...).SetState(downloading) write is rejected. Install it AFTER
// seeding so the fixture rows are unaffected; it targets exclusively the
// state→downloading update, leaving any other chapter write untouched.
func installClaimWriteFailure(client *ent.Client) {
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if cm, ok := m.(*ent.ChapterMutation); ok && cm.Op().Is(ent.OpUpdateOne|ent.OpUpdate) {
				if st, exists := cm.State(); exists && st == entchapter.StateDownloading {
					return nil, errors.New("injected write failure: chapter state→downloading")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
}

// TestRunOnce_ClaimWriteFails_ReturnsZeroProgress is the accounting proof behind
// the drain-loop termination fix: when the atomic wanted→downloading claim WRITE
// fails (reads still succeed), RunOnce must return dispatched==0 (no forward
// progress) EVEN THOUGH a live-candidate chapter was SELECTED into a source group.
// A selection-based count would return >=1 here and hot-spin the drain loop
// (job.Runner.RunDownloadCycle) forever under a write-failing DB. The chapter must
// remain wanted (it never left the actionable set), and no error is surfaced (the
// per-chapter failure is logged, not propagated).
func TestRunOnce_ClaimWriteFails_ReturnsZeroProgress(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Write Fail").SetSlug("write-fail-claim").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("src").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("https://src/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	// Fail the claim write AFTER seeding — only the state→downloading update fails.
	installClaimWriteFailure(client)

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	dispatched, err := d.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce must not surface a per-chapter write failure: %v", err)
	}
	if dispatched != 0 {
		t.Errorf("dispatched = %d, want 0 (the wanted→downloading claim write failed → no forward progress)", dispatched)
	}
	if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateWanted {
		t.Errorf("chapter state = %s, want wanted (claim failed, chapter must not advance)", got)
	}
}
