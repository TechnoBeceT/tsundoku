// Package download_test — proofs for the BATCHED, no-N+1 upgrade detection
// (GAP-112). detectUpgrades replaced a per-chapter chapter.RankedLiveCandidates
// (a ~30k-query N+1 on a large library, run every download cycle) with ONE bulk
// candidate load plus ONE circuit-breaker snapshot, while flagging the EXACT same
// chapter set. Two tests pin that:
//
//   - TestDetectUpgrades_BatchedFlagsExactSameSet: a mixed library exercising every
//     carve-out at once (heal, park-0, removed-frozen, self-churn, strict >, and a
//     cross-series same-string chapter_key) — the batched scan flags precisely the
//     chapters the documented rules require and no others.
//   - TestDetectUpgrades_QueryCountIsLibrarySizeIndependent: the read-query count is
//     IDENTICAL for a small and a large library — the definitive N+1 guard (the
//     older slope test only ceilinged the per-chapter cost at 3).
//
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// TestDetectUpgrades_BatchedFlagsExactSameSet seeds one library that hits every
// detection carve-out in a SINGLE scan and asserts the batched detectUpgrades
// flags EXACTLY the expected chapters — proving the bulk path's per-chapter
// mapping (and its (series_id, chapter_key) bucketing) reproduces the
// single-chapter rules byte-for-byte, including cross-series key isolation.
func TestDetectUpgrades_BatchedFlagsExactSameSet(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// --- Case 1: demoted satisfier, a strictly higher DIFFERENT source → FLAG ---
	sA := client.Series.Create().SetTitle("batch-demoted").SetSlug("batch-demoted").SaveX(ctx)
	spDemoted := seedSource(ctx, t, client, sA, "b-demoted-low", 20, "k-demote")
	seedSource(ctx, t, client, sA, "b-demoted-high", 60, "k-demote")
	chFlagDemote := seedDownloadedChapter(ctx, t, client, sA, "k-demote", &spDemoted.ID, 60)

	// --- Case 2: only the (demoted) satisfier remains, no better source → NO flag ---
	sB := client.Series.Create().SetTitle("batch-nobetter").SetSlug("batch-nobetter").SaveX(ctx)
	spOnly := seedSource(ctx, t, client, sB, "b-only", 20, "k-only")
	chNoBetter := seedDownloadedChapter(ctx, t, client, sB, "k-only", &spOnly.ID, 20)

	// --- Case 3: PARKED (importance-0) satisfier + inferior sibling → NO flag ---
	sC := client.Series.Create().SetTitle("batch-parked").SetSlug("batch-parked").SaveX(ctx)
	spParked := seedSource(ctx, t, client, sC, "b-parked", 40, "k-park")
	seedSource(ctx, t, client, sC, "b-parked-inferior", 20, "k-park")
	chParked := seedDownloadedChapter(ctx, t, client, sC, "k-park", &spParked.ID, 40)
	client.SeriesProvider.UpdateOneID(spParked.ID).SetImportance(0).ExecX(ctx) // library merge park

	// --- Case 4: removed satisfier (nil), frozen watermark, EQUAL source → NO flag ---
	sD := client.Series.Create().SetTitle("batch-removed-eq").SetSlug("batch-removed-eq").SaveX(ctx)
	seedSource(ctx, t, client, sD, "b-removed-eq", 60, "k-req")
	chRemovedEq := seedDownloadedChapter(ctx, t, client, sD, "k-req", nil, 60)

	// --- Case 5: removed satisfier (nil), frozen watermark, STRICTLY HIGHER → FLAG ---
	sE := client.Series.Create().SetTitle("batch-removed-hi").SetSlug("batch-removed-hi").SaveX(ctx)
	seedSource(ctx, t, client, sE, "b-removed-hi", 70, "k-rhi")
	chFlagRemovedHi := seedDownloadedChapter(ctx, t, client, sE, "k-rhi", nil, 60)

	// --- Case 6: self-churn — best live source IS the satisfier (stale-low watermark) → NO flag ---
	sF := client.Series.Create().SetTitle("batch-selfchurn").SetSlug("batch-selfchurn").SaveX(ctx)
	spSelf := seedSource(ctx, t, client, sF, "b-self", 50, "k-self")
	chSelfChurn := seedDownloadedChapter(ctx, t, client, sF, "k-self", &spSelf.ID, 30)

	// --- Case 7: cross-series SAME chapter_key. sG has no better source (→ NO flag);
	// sH shares the identical key string but DOES have a higher source (→ FLAG).
	// If bucketing were by key alone, sG's chapter would wrongly see sH's higher source.
	sG := client.Series.Create().SetTitle("batch-collide-g").SetSlug("batch-collide-g").SaveX(ctx)
	spG := seedSource(ctx, t, client, sG, "b-collide-g", 10, "collide")
	chCollideG := seedDownloadedChapter(ctx, t, client, sG, "collide", &spG.ID, 10)

	sH := client.Series.Create().SetTitle("batch-collide-h").SetSlug("batch-collide-h").SaveX(ctx)
	spH := seedSource(ctx, t, client, sH, "b-collide-h", 10, "collide")
	seedSource(ctx, t, client, sH, "b-collide-h-hi", 30, "collide")
	chFlagCollideH := seedDownloadedChapter(ctx, t, client, sH, "collide", &spH.ID, 10)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}

	wantFlagged := []*ent.Chapter{chFlagDemote, chFlagRemovedHi, chFlagCollideH}
	if n != len(wantFlagged) {
		t.Errorf("DetectUpgrades flagged %d, want %d", n, len(wantFlagged))
	}
	assertExactFlaggedSet(ctx, t, client, wantFlagged)

	// The chapters that must NOT flag stay downloaded.
	assertDownloaded(ctx, t, client, map[string]*ent.Chapter{
		"no-better":      chNoBetter,
		"parked":         chParked,
		"removed-equal":  chRemovedEq,
		"self-churn":     chSelfChurn,
		"cross-series-g": chCollideG,
	})

	// Stale watermarks are healed to the satisfier's CURRENT importance even when the
	// chapter is not (self-churn) or is (demoted) flagged — the batched path keeps the
	// per-chapter heal-write. A PARKED satisfier's watermark must NOT be healed down to
	// the 0 sentinel.
	assertWatermark(ctx, t, client, chSelfChurn, 50, "self-churn (healed to satisfier's current importance)")
	assertWatermark(ctx, t, client, chFlagDemote, 20, "demoted (healed to satisfier's current importance)")
	assertWatermark(ctx, t, client, chParked, 40, "parked (frozen — never healed down to the park sentinel 0)")
}

// assertExactFlaggedSet fails unless the chapters currently in upgrade_available
// are EXACTLY want — every expected one flagged, and no unexpected extra.
func assertExactFlaggedSet(ctx context.Context, t *testing.T, client *ent.Client, want []*ent.Chapter) {
	t.Helper()
	wantIDs := map[string]bool{}
	for _, ch := range want {
		wantIDs[ch.ID.String()] = true
	}
	gotIDs := map[string]bool{}
	for _, ch := range client.Chapter.Query().Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable)).AllX(ctx) {
		gotIDs[ch.ID.String()] = true
		if !wantIDs[ch.ID.String()] {
			t.Errorf("chapter %s was flagged upgrade_available but should NOT be", ch.ID)
		}
	}
	for id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("chapter %s should be flagged upgrade_available but was not", id)
		}
	}
}

// assertDownloaded fails unless every named chapter is still state=downloaded.
func assertDownloaded(ctx context.Context, t *testing.T, client *ent.Client, chapters map[string]*ent.Chapter) {
	t.Helper()
	for name, ch := range chapters {
		if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateDownloaded {
			t.Errorf("%s chapter state = %s, want downloaded (must not flag)", name, got)
		}
	}
}

// assertWatermark fails unless ch's satisfied_importance equals want.
func assertWatermark(ctx context.Context, t *testing.T, client *ent.Client, ch *ent.Chapter, want int, why string) {
	t.Helper()
	if wm := client.Chapter.GetX(ctx, ch.ID).SatisfiedImportance; wm == nil || *wm != want {
		t.Errorf("%s watermark = %v, want %d", why, wm, want)
	}
}

// batchCountingDriver wraps an Ent SQL driver and counts every READ query issued
// through it (eager-loading sub-queries included). Writes go through Exec and are
// not counted — so a heal-write or a flag SetState never inflates the read count.
// Test-only: it PROVES detectUpgrades' read count does not grow with library size.
type batchCountingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

// Query counts the read and delegates.
func (d *batchCountingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

// newBatchCountingClient builds a second Ent client over the SAME test database
// whose reads are counted.
func newBatchCountingClient(db *sql.DB) (*ent.Client, *batchCountingDriver) {
	drv := &batchCountingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(drv)), drv
}

// seedNoOpDownloadedSeries creates n series, each with one downloaded chapter
// satisfied by its only source at a watermark that already matches the source's
// importance — so a scan neither heals nor flags any of them, leaving ONLY the
// bounded batch reads to count.
func seedNoOpDownloadedSeries(ctx context.Context, t *testing.T, client *ent.Client, prefix string, n int) {
	t.Helper()
	for i := range n {
		slug := prefix + "-" + string(rune('a'+i))
		s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
		sp := seedSource(ctx, t, client, s, "src-"+slug, 10, "key-"+slug)
		seedDownloadedChapter(ctx, t, client, s, "key-"+slug, &sp.ID, 10)
	}
}

// TestDetectUpgrades_QueryCountIsLibrarySizeIndependent is the definitive no-N+1
// proof: it counts the SQL reads a full detectUpgrades scan issues for a small
// library and again for a much larger one. The counts must be IDENTICAL — the
// batched candidate load plus the single breaker snapshot cost a constant number
// of reads regardless of how many downloaded chapters are scanned. A regression to
// the per-chapter RankedLiveCandidates (or a per-candidate gate query) would make
// the larger library cost many more reads and fail this test.
func TestDetectUpgrades_QueryCountIsLibrarySizeIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)

	client, drv := newBatchCountingClient(db)

	count := func() int64 {
		drv.queries.Store(0)
		if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
			t.Fatalf("DetectUpgrades: %v", err)
		}
		return drv.queries.Load()
	}

	// Small library.
	seedNoOpDownloadedSeries(ctx, t, seedClient, "qc-small", 3)
	small := count()

	// Grow the library ~7x with more no-op downloaded chapters.
	seedNoOpDownloadedSeries(ctx, t, seedClient, "qc-large", 18)
	large := count()

	if small != large {
		t.Errorf("N+1: detectUpgrades issued %d reads for 3 chapters but %d for 21 — the read count must not scale with library size", small, large)
	}
	const maxReads = 6 // downloaded-chapters query (+satisfied_by eager) + batch feed load (+series_provider eager)
	if large > maxReads {
		t.Errorf("detectUpgrades issued %d reads for one scan, want <= %d (bounded, library-size independent)", large, maxReads)
	}
	t.Logf("reads: 3 chapters=%d, 21 chapters=%d", small, large)
}
