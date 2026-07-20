// Package download_test — the ban-loop regression proofs for the SHARED
// per-source per-cycle fetch budget (downloads + upgrades draw from ONE budget
// per physical source per cycle, downloads first).
//
// Root cause it guards: a library with hundreds of chapters all flagged
// upgrade_available toward ONE higher source (a fallback source's whole backlog)
// used to be fetched in a single UpgradeAll pass — the upgrade pass capped
// CONCURRENCY but had no per-cycle VOLUME cap — re-banning the source, whose
// failed upgrades reverted to downloaded, got re-flagged, and looped forever. The
// fix bounds each physical source to C = batchPerSource(DownloadConcurrency)
// FETCHES per cycle across downloads AND upgrades combined, so the flagged count
// converges by at most C per cycle instead of flooding.
//
// Requires Docker (via testcontainers).
package download_test

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
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// seedFlaggedUpgradesToSource creates n independent series, each already
// downloaded from a low-importance provider ("low", importance 2) and ALREADY
// flagged upgrade_available toward the SAME strictly-higher provider string
// highProvider (importance 10). Every chapter therefore resolves the SAME
// canonicalSourceKey(highProvider) as its upgrade target, so groupByUpgradeTarget
// puts them in ONE group — the shared-source scenario the per-cycle volume cap
// bounds. Returns the chapter ids.
func seedFlaggedUpgradesToSource(ctx context.Context, t *testing.T, client *ent.Client, slugPrefix, highProvider string, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, n)
	for i := range n {
		slug := fmt.Sprintf("%s-%d", slugPrefix, i)
		key := slug
		s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
		spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetImportance(2).SaveX(ctx)
		client.ProviderChapter.Create().
			SetSeriesProviderID(spLow.ID).SetChapterKey(key).SetURL("https://low/" + key).SetProviderIndex(0).SaveX(ctx)
		spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider(highProvider).SetImportance(10).SaveX(ctx)
		client.ProviderChapter.Create().
			SetSeriesProviderID(spHigh.ID).SetChapterKey(key).SetURL("https://high/" + key).SetProviderIndex(0).SaveX(ctx)
		ch := client.Chapter.Create().
			SetSeries(s).SetChapterKey(key).
			SetState(entchapter.StateUpgradeAvailable).
			SetSatisfiedByID(spLow.ID).
			SetSatisfiedImportance(2).
			SaveX(ctx)
		ids = append(ids, ch.ID)
	}
	return ids
}

// TestUpgradeAll_PerSourceVolumeCap_LeavesRemainderFlagged is THE core ban-loop
// regression proof: with N=25 chapters all flagged upgrade_available toward one
// source and C=batchPerSource(5)=10, a single UpgradeAll pass upgrades EXACTLY 10
// (10 fetch attempts to that source) and leaves the other 15 upgrade_available for
// a later cycle — instead of the pre-fix behaviour that fetched all 25 in one
// cycle and re-banned the source.
func TestUpgradeAll_PerSourceVolumeCap_LeavesRemainderFlagged(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		concurrency = 5
		budgetC     = 2 * concurrency // batchPerSource(5) = 10
		n           = 25
	)
	ids := seedFlaggedUpgradesToSource(ctx, t, client, "ban-loop", "Asura", n)

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: concurrency}, nil)

	// nil consumed: no downloads ran this cycle, so the whole per-cycle budget is
	// available to upgrades.
	upgraded, err := d.UpgradeAll(ctx, nil)
	if err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	if upgraded != budgetC {
		t.Errorf("upgraded = %d, want %d (the per-source per-cycle budget) — a source's whole backlog must NOT upgrade in one cycle", upgraded, budgetC)
	}

	counts := countStates(ctx, t, client, ids)
	if counts[entchapter.StateDownloaded] != budgetC {
		t.Errorf("downloaded = %d, want %d", counts[entchapter.StateDownloaded], budgetC)
	}
	if counts[entchapter.StateUpgradeAvailable] != n-budgetC {
		t.Errorf("still upgrade_available = %d, want %d (deferred to a later cycle)", counts[entchapter.StateUpgradeAvailable], n-budgetC)
	}
}

// TestUpgradeAll_SharesBudgetWithDownloads proves downloads and upgrades draw from
// ONE per-source budget: when the download drain already fetched 4 of the source's
// budget this cycle (downloadsConsumed["Asura"]=4), only the REMAINING 6 of C=10
// are available to upgrades — so the source is fetched at most C times total this
// cycle (4 downloads + 6 upgrades), never more.
func TestUpgradeAll_SharesBudgetWithDownloads(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		concurrency        = 5
		budgetC            = 2 * concurrency // 10
		downloadsThisCycle = 4
		wantUpgrades       = budgetC - downloadsThisCycle // 6
		n                  = 25
	)
	ids := seedFlaggedUpgradesToSource(ctx, t, client, "shared-budget", "Asura", n)

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: concurrency}, nil)

	// The download drain of this cycle already spent 4 of "Asura"'s budget.
	consumed := map[string]int{"Asura": downloadsThisCycle}
	upgraded, err := d.UpgradeAll(ctx, consumed)
	if err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	if upgraded != wantUpgrades {
		t.Errorf("upgraded = %d, want %d (C - downloads already fetched from this source)", upgraded, wantUpgrades)
	}
	counts := countStates(ctx, t, client, ids)
	if counts[entchapter.StateDownloaded] != wantUpgrades {
		t.Errorf("downloaded = %d, want %d", counts[entchapter.StateDownloaded], wantUpgrades)
	}
	if counts[entchapter.StateUpgradeAvailable] != n-wantUpgrades {
		t.Errorf("still upgrade_available = %d, want %d", counts[entchapter.StateUpgradeAvailable], n-wantUpgrades)
	}
}

// TestRunOnceAt_CapsSourceAtBudgetAcrossPasses proves the DOWNLOAD side of the
// shared cap: a source with 50 wanted chapters, drained through the SAME shared
// consumed map across however many RunOnceAt passes (mirroring
// job.Runner.drainDownloads), dispatches at most C=batchPerSource(5)=10 this cycle
// — the rest stay wanted for the next cycle. Without the cross-pass budget the
// drain loop dispatched a source's whole backlog per cycle.
func TestRunOnceAt_CapsSourceAtBudgetAcrossPasses(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		concurrency = 5
		budgetC     = 2 * concurrency // 10
		n           = 50
	)
	ids := seedSourceChapters(ctx, t, client, "big-backlog", "Asura", 10, n)

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: concurrency}, nil)

	// The drain loop: repeatedly call RunOnceAt with the SAME shared budget map
	// (snapshotting now once), exactly as job.Runner.drainDownloads does.
	now := time.Now()
	consumed := make(map[string]int)
	total := 0
	for pass := 0; pass < 20; pass++ {
		dispatched, err := d.RunOnceAt(ctx, now, consumed)
		if err != nil {
			t.Fatalf("pass %d RunOnceAt: %v", pass, err)
		}
		total += dispatched
		if dispatched == 0 {
			break
		}
	}

	if total != budgetC {
		t.Errorf("total dispatched this cycle = %d, want %d (the per-source per-cycle budget across all passes)", total, budgetC)
	}
	if consumed["Asura"] != budgetC {
		t.Errorf("consumed[Asura] = %d, want %d", consumed["Asura"], budgetC)
	}
	counts := countStates(ctx, t, client, ids)
	if counts[entchapter.StateDownloaded] != budgetC {
		t.Errorf("downloaded = %d, want %d after one cycle", counts[entchapter.StateDownloaded], budgetC)
	}
	if counts[entchapter.StateWanted] != n-budgetC {
		t.Errorf("wanted = %d, want %d (deferred to the next cycle)", counts[entchapter.StateWanted], n-budgetC)
	}
}

// TestUpgradeAll_ConvergesByBudgetPerCycle proves the loop CONVERGES instead of
// flooding: driving UpgradeAll cycle after cycle over a fixed backlog of 25
// chapters all flagged toward one source, the upgrade_available count drops by AT
// MOST C=10 each cycle and reaches 0 — the exact anti-flood property the ban loop
// violated (its "flagged=946 upgraded=946" repeated forever).
func TestUpgradeAll_ConvergesByBudgetPerCycle(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		concurrency = 5
		budgetC     = 2 * concurrency // 10
		n           = 25
	)
	ids := seedFlaggedUpgradesToSource(ctx, t, client, "converge", "Asura", n)

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: concurrency}, nil)

	prevRemaining := n
	converged := false
	for cycle := 1; cycle <= 10; cycle++ {
		upgraded, err := d.UpgradeAll(ctx, nil)
		if err != nil {
			t.Fatalf("cycle %d UpgradeAll: %v", cycle, err)
		}
		if upgraded > budgetC {
			t.Fatalf("cycle %d upgraded %d, want <= %d (a cycle must not flood the source)", cycle, upgraded, budgetC)
		}
		remaining := countStates(ctx, t, client, ids)[entchapter.StateUpgradeAvailable]
		if drop := prevRemaining - remaining; drop > budgetC {
			t.Fatalf("cycle %d dropped %d upgrade_available, want <= %d (paced by the per-cycle budget)", cycle, drop, budgetC)
		}
		prevRemaining = remaining
		if remaining == 0 {
			converged = true
			break
		}
	}
	if !converged {
		t.Fatalf("upgrade_available never converged to 0 within 10 cycles (still %d) — the loop is flooding, not converging", prevRemaining)
	}
}

// TestUpgradeAll_CrossSourceFairness_EachSourceGetsBudget proves the cap is PER
// SOURCE, not global: two different upgrade-target sources, each with 15 chapters
// flagged toward it, EACH get their own full budget C=10 in one cycle (total 20
// upgrades) — one source's backlog never steals another's budget.
func TestUpgradeAll_CrossSourceFairness_EachSourceGetsBudget(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		concurrency = 5
		budgetC     = 2 * concurrency // 10
		perSource   = 15
	)
	idsA := seedFlaggedUpgradesToSource(ctx, t, client, "fair-asura", "Asura", perSource)
	idsB := seedFlaggedUpgradesToSource(ctx, t, client, "fair-comix", "Comix", perSource)

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: concurrency}, nil)

	upgraded, err := d.UpgradeAll(ctx, nil)
	if err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	if upgraded != 2*budgetC {
		t.Errorf("upgraded = %d, want %d (each of the two sources gets its own budget of %d)", upgraded, 2*budgetC, budgetC)
	}
	for _, tc := range []struct {
		name string
		ids  []uuid.UUID
	}{{"Asura", idsA}, {"Comix", idsB}} {
		counts := countStates(ctx, t, client, tc.ids)
		if counts[entchapter.StateDownloaded] != budgetC {
			t.Errorf("source %s: downloaded = %d, want %d (its own full budget)", tc.name, counts[entchapter.StateDownloaded], budgetC)
		}
		if counts[entchapter.StateUpgradeAvailable] != perSource-budgetC {
			t.Errorf("source %s: still upgrade_available = %d, want %d", tc.name, counts[entchapter.StateUpgradeAvailable], perSource-budgetC)
		}
	}
}
