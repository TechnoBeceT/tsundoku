package library_test

import (
	"context"
	"database/sql"
	"sort"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// healSvc builds the library service the self-heal needs: db + storage root +
// a trigger spy. Ingest/imports/series are unused by the dedup/merge core.
func healSvc(client *ent.Client, storage string, triggered *int) *library.Service {
	return library.NewService(client, nil, nil, nil, func() { *triggered++ }, storage, sse.NewHub())
}

// attachLinkedTwin creates a LINKED SeriesProvider (numeric provider id ⇒
// series.IsLinkedProvider true) on seriesID whose resolved display name is
// providerName under scanlator, optionally carrying a ProviderChapter feed for
// keys "1"/"2". withFeed=false reproduces the production defect: the live source
// was broken when the owner matched it, so it ingested nothing.
func attachLinkedTwin(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID, providerName, scanlator string, importance int, withFeed bool) *ent.SeriesProvider {
	t.Helper()
	live := client.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider("99").
		SetProviderName(providerName).
		SetScanlator(scanlator).
		SetImportance(importance).
		SaveX(ctx)
	if withFeed {
		one, two := 1.0, 2.0
		client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)
	}
	return live
}

// importedDiskSeries writes + imports one disk-origin series (2 CBZs) and
// returns its row. The imported provider's `provider` column holds the display
// NAME, which is exactly what makes it unlinked.
func importedDiskSeries(t *testing.T, client *ent.Client, storage, title, providerName, scanlator string) *ent.Series {
	t.Helper()
	ctx := context.Background()
	writeKaizokuSeries(t, storage, "Manga", title, providerName, scanlator, 2)
	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("disk.ScanLibrary: %v", err)
	}
	// Import by TITLE, not facts[0] — this helper is called more than once per
	// storage root, so a positional pick would re-import the first series.
	for _, sf := range facts {
		if sf.Title == title {
			importOneFromFacts(t, client, sf)
		}
	}
	for _, s := range client.Series.Query().AllX(ctx) {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("imported series %q not found", title)
	return nil
}

// TestDriftedSeriesIDs_SelectsOnlyUnlinkedDiskOrigin is the targeting proof: the
// query returns exactly the series carrying an UNLINKED disk-origin provider and
// nothing else. A fully-linked series (every provider is a numeric source id) is
// NOT selected even though it has providers, and a series with TWO disk-origin
// rows is returned ONCE.
//
// FAILS on the unfixed code: driftedSeriesIDs does not exist there — before this
// change the recurring pass had no way to narrow its input, which is why
// DedupAllProviders' walk-every-series shape was the only option.
func TestDriftedSeriesIDs_SelectsOnlyUnlinkedDiskOrigin(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)

	// Series A: one disk-origin provider (from the import) — a candidate.
	a := importedDiskSeries(t, client, storage, "Drifted A", "mangadex", "Alpha")
	// Series B: TWO disk-origin providers — still exactly one candidate id.
	b := importedDiskSeries(t, client, storage, "Drifted B", "weebcentral", "Beta")
	client.SeriesProvider.Create().
		SetSeriesID(b.ID).SetProvider("Second Disk Name").SetImportance(1).SaveX(ctx)
	// Series C: fully linked — two numeric-id providers, no disk-origin row.
	c := client.Series.Create().SetTitle("All Linked").SetSlug("all-linked").SaveX(ctx)
	client.SeriesProvider.Create().SetSeriesID(c.ID).SetProvider("7").SetImportance(10).SaveX(ctx)
	client.SeriesProvider.Create().SetSeriesID(c.ID).SetProvider(" 8 ").SetImportance(20).SaveX(ctx)
	// Series D: no providers at all — never a candidate.
	client.Series.Create().SetTitle("No Providers").SetSlug("no-providers").SaveX(ctx)

	got, err := healSvc(client, storage, new(int)).DriftedSeriesIDs(ctx)
	if err != nil {
		t.Fatalf("DriftedSeriesIDs: %v", err)
	}

	want := []uuid.UUID{a.ID, b.ID}
	sortIDs(got)
	sortIDs(want)
	if len(got) != len(want) {
		t.Fatalf("selected %d series (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selected = %v, want %v (only the series with an unlinked disk-origin provider)", got, want)
		}
	}
}

// TestHealDriftedProviders_MergesDriftedPairWithFeed is the core self-heal proof
// and the exact production scenario: a disk-imported series plus a linked twin
// for the SAME physical source whose feed refresh has now populated. One
// unattended pass must fold them into ONE provider, re-point both downloaded
// chapters onto the live source, keep both CBZs (relabeled, never deleted), and
// leave importance == satisfied_importance so upgrade detection cannot fire.
//
// FAILS on the unfixed code: HealDriftedProviders does not exist, so nothing ever
// retried the merge that merge-at-attach had declined — the two rows coexisted
// forever and the disk row's chapter URLs stayed frozen.
func TestHealDriftedProviders_MergesDriftedPairWithFeed(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)

	ser := importedDiskSeries(t, client, storage, "My Series", "mangadex", "Alpha")
	live := attachLinkedTwin(t, client, ctx, ser.ID, "mangadex", "Alpha", 5, true)

	triggered := 0
	merged, skipped, err := healSvc(client, storage, &triggered).HealDriftedProviders(ctx)
	if err != nil {
		t.Fatalf("HealDriftedProviders: %v", err)
	}
	if merged != 1 || skipped != 0 {
		t.Fatalf("merged/skipped = %d/%d, want 1/0", merged, skipped)
	}
	if triggered == 0 {
		t.Error("a successful merge must fire the download-cycle trigger")
	}

	assertProviderCount(t, client, ctx, ser.ID, 1)
	remaining := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)
	if remaining.ID != live.ID {
		t.Fatalf("surviving provider = %s, want the linked twin %s", remaining.ID, live.ID)
	}

	assertRepointed(t, client, ctx, ser.ID, remaining)
}

// assertRepointed pins the post-merge chapter state: every chapter of the series
// is satisfied by `live` at `live.Importance` (so importance ==
// satisfied_importance and upgrade detection's strict `>` can never fire), still
// has a filename (the CBZ was relabeled and KEPT, never removed), and none is
// flagged upgrade_available.
func assertRepointed(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID, live *ent.SeriesProvider) {
	t.Helper()
	chapters := client.Chapter.Query().Where(entchapter.SeriesID(seriesID)).AllX(ctx)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2 (a merge never deletes a chapter)", len(chapters))
	}
	for _, ch := range chapters {
		if ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != live.ID {
			t.Errorf("chapter %s satisfied_by = %v, want the live twin", ch.ChapterKey, ch.SatisfiedByProviderID)
		}
		if ch.SatisfiedImportance == nil || *ch.SatisfiedImportance != live.Importance {
			t.Errorf("chapter %s satisfied_importance = %v, want %d (== provider importance, so detection cannot flag it)",
				ch.ChapterKey, ch.SatisfiedImportance, live.Importance)
		}
		if ch.Filename == "" {
			t.Errorf("chapter %s lost its filename — the CBZ must be relabeled and KEPT, never removed", ch.ChapterKey)
		}
	}
	if flagged := client.Chapter.Query().
		Where(entchapter.SeriesID(seriesID), entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
		CountX(ctx); flagged != 0 {
		t.Errorf("upgrade_available chapters = %d, want 0 (the heal must not arm a re-download)", flagged)
	}
}

// TestHealDriftedProviders_EmptyLiveFeedIsSkipped is the ORPHAN GUARD: the
// linked twin name-matches the disk row but its ProviderChapter feed is still
// EMPTY (the live source was broken when the owner matched it — the production
// cause). Merging there would relabel nothing and then delete the disk row,
// orphaning the downloaded chapters' provenance. The automatic pass must decline
// exactly as merge-at-attach and the owner path do: both rows survive, the
// chapters stay satisfied by the disk provider, and nothing is triggered.
//
// FAILS on any implementation that heals by bypassing pickTwin/providerHasFeed
// (e.g. merging into the first name-matching twin) — the disk provider row would
// be gone and its chapters left with a nil satisfied_by.
func TestHealDriftedProviders_EmptyLiveFeedIsSkipped(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)

	ser := importedDiskSeries(t, client, storage, "My Series", "mangadex", "Alpha")
	diskSP := diskProviderOf(t, client, ctx, ser.ID)
	attachLinkedTwin(t, client, ctx, ser.ID, "mangadex", "Alpha", 5, false)

	triggered := 0
	merged, skipped, err := healSvc(client, storage, &triggered).HealDriftedProviders(ctx)
	if err != nil {
		t.Fatalf("HealDriftedProviders: %v", err)
	}
	if merged != 0 || skipped != 1 {
		t.Fatalf("merged/skipped = %d/%d, want 0/1 (the empty-feed pair must be skipped, not merged)", merged, skipped)
	}
	if triggered != 0 {
		t.Errorf("trigger fired %d time(s); a skipped pair changes nothing so it must not trigger", triggered)
	}

	assertProviderCount(t, client, ctx, ser.ID, 2)
	if !client.SeriesProvider.Query().Where(seriesprovider.IDEQ(diskSP.ID)).ExistX(ctx) {
		t.Fatal("the disk-origin provider was deleted — merging into an empty feed orphans its chapters")
	}
	for _, ch := range client.Chapter.Query().Where(entchapter.SeriesID(ser.ID)).AllX(ctx) {
		if ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != diskSP.ID {
			t.Errorf("chapter %s satisfied_by = %v, want the surviving disk provider", ch.ChapterKey, ch.SatisfiedByProviderID)
		}
	}
}

// TestHealDriftedProviders_NameMismatchIsLeftAloneAndCostsNothingExtra covers the
// permanently-unmatchable row: a disk provider named "KaliScan.me" next to a live
// source whose display name is "KaliScan". providerNameMatches is exact
// (case-insensitive, trimmed) equality, so that pair can never fold — and a
// recurring pass must therefore be cheap AND stable for it forever.
//
// It asserts two things:
//  1. Nothing changes — both rows survive, merged/skipped are both 0 (a
//     name-mismatch is not even a "skip"), and no trigger fires.
//  2. The SQL cost of a pass is IDENTICAL on the second run, i.e. nothing
//     accumulates: no growing skip-list, no re-walk that gets longer, no thrash.
//     A generous ceiling still catches a per-chapter or per-provider fan-out.
//
// This is partly a GUARD (the name-match rule is pre-existing and unchanged), but
// the cost half is genuinely new: the unfixed code never ran this pass at all, so
// there was no per-sweep cost to bound.
func TestHealDriftedProviders_NameMismatchIsLeftAloneAndCostsNothingExtra(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	seedClient, db := testdb.NewWithSQL(t)

	ser := importedDiskSeries(t, seedClient, storage, "Kali Series", "KaliScan.me", "")
	diskSP := diskProviderOf(t, seedClient, ctx, ser.ID)
	live := attachLinkedTwin(t, seedClient, ctx, ser.ID, "KaliScan", "", 5, true)

	client, drv := newHealCountingClient(db)
	triggered := 0
	svc := healSvc(client, storage, &triggered)

	pass := func() int64 {
		drv.queries.Store(0)
		merged, skipped, err := svc.HealDriftedProviders(ctx)
		if err != nil {
			t.Fatalf("HealDriftedProviders: %v", err)
		}
		if merged != 0 || skipped != 0 {
			t.Fatalf("merged/skipped = %d/%d, want 0/0 — %q can never match %q", merged, skipped, "KaliScan.me", "KaliScan")
		}
		return drv.queries.Load()
	}

	first, second := pass(), pass()
	if first != second {
		t.Errorf("pass cost grew: %d queries then %d — an unmatchable row must cost the SAME every sweep (no accumulated work)", first, second)
	}
	const maxQueries = 6 // targeting read + series load + provider list, with slack
	if first > maxQueries {
		t.Errorf("one pass issued %d queries for an unmatchable row, want <= %d", first, maxQueries)
	}
	t.Logf("queries per pass: first=%d second=%d", first, second)

	if triggered != 0 {
		t.Errorf("trigger fired %d time(s); nothing changed so nothing must be triggered", triggered)
	}
	assertProviderCount(t, seedClient, ctx, ser.ID, 2)
	if !seedClient.SeriesProvider.Query().Where(seriesprovider.IDEQ(diskSP.ID)).ExistX(ctx) {
		t.Error("the unmatchable disk provider was removed — a name mismatch must never merge")
	}
	if !seedClient.SeriesProvider.Query().Where(seriesprovider.IDEQ(live.ID)).ExistX(ctx) {
		t.Error("the live source row was removed — a name mismatch must never merge")
	}
}

// TestHealDriftedProviders_NoDiskOriginProviderIsUntouchedAndFree proves the scope
// limit and the cheapness claim together: a library whose every provider is a real
// linked source is left completely alone, and the whole pass costs exactly ONE
// query (the targeting read) — it short-circuits before loading a single series.
//
// FAILS on the unfixed code's shape: DedupAllProviders walks every series id and
// issues a series load + a provider list PER SERIES, so this would be many
// queries, growing with library size.
func TestHealDriftedProviders_NoDiskOriginProviderIsUntouchedAndFree(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	seedClient, db := testdb.NewWithSQL(t)

	for i, title := range []string{"Linked One", "Linked Two", "Linked Three"} {
		s := seedClient.Series.Create().SetTitle(title).SetSlug(title).SaveX(ctx)
		seedClient.SeriesProvider.Create().
			SetSeriesID(s.ID).SetProvider("9").SetProviderName("mangadex").
			SetImportance(10 * (i + 1)).SaveX(ctx)
	}

	client, drv := newHealCountingClient(db)
	triggered := 0
	drv.queries.Store(0)
	merged, skipped, err := healSvc(client, storage, &triggered).HealDriftedProviders(ctx)
	if err != nil {
		t.Fatalf("HealDriftedProviders: %v", err)
	}
	if merged != 0 || skipped != 0 {
		t.Fatalf("merged/skipped = %d/%d, want 0/0 (no disk-origin provider anywhere)", merged, skipped)
	}
	if triggered != 0 {
		t.Errorf("trigger fired %d time(s) on a no-op pass", triggered)
	}
	if got := drv.queries.Load(); got != 1 {
		t.Errorf("a nothing-to-heal pass issued %d queries, want exactly 1 (the targeting read alone)", got)
	}
	if n := seedClient.SeriesProvider.Query().CountX(ctx); n != 3 {
		t.Errorf("SeriesProvider rows = %d, want 3 — a fully-linked library must be untouched", n)
	}
}

// diskProviderOf returns the series' single UNLINKED disk-origin provider.
func diskProviderOf(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID) *ent.SeriesProvider {
	t.Helper()
	for _, p := range client.SeriesProvider.Query().Where(seriesprovider.SeriesID(seriesID)).AllX(ctx) {
		if !series.IsLinkedProvider(p) {
			return p
		}
	}
	t.Fatal("no disk-origin provider on the series")
	return nil
}

// sortIDs orders uuids so a set comparison is order-independent.
func sortIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
}

// healCountingDriver counts every read query issued through it. Test-only: it
// exists to PROVE the self-heal pass is ~free when nothing needs healing and that
// an unmatchable row costs the same on every sweep. Mirrors internal/series'
// countingDriver.
type healCountingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

// Query counts the read and delegates.
func (d *healCountingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

// newHealCountingClient builds a second Ent client over the SAME test database
// whose reads are counted.
func newHealCountingClient(db *sql.DB) (*ent.Client, *healCountingDriver) {
	drv := &healCountingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(drv)), drv
}
