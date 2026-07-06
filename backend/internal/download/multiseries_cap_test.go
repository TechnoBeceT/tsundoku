// Package download_test — reproduction for the per-source concurrency-cap bug
// when MULTIPLE SERIES share one source ID.
package download_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/sse"
)

// seedSourceChaptersScanlator is like seedSourceChapters but lets the caller set a
// scanlator on the SeriesProvider, so a single source ID can back multiple
// (source,scanlator) providers within a series.
func seedSourceChaptersScanlator(ctx context.Context, t *testing.T, client *ent.Client, slug, provider, scanlator string, importance, n int) []uuid.UUID {
	t.Helper()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetScanlator(scanlator).SetImportance(importance).SaveX(ctx)
	return seedProviderChapters(ctx, t, client, s, sp, slug, provider, n)
}

// seedNamedSourceChapters is like seedSourceChapters but also stores a
// provider_name on the SeriesProvider. It mirrors the Suwayomi ingest path, which
// records the source's numeric id in `provider` AND its human display name in
// `provider_name` (internal/suwayomi/ingest.go). The disk-reconcile path instead
// stores the display name directly in `provider` with an EMPTY `provider_name`
// (internal/disk/reconcile.go). A physical source is therefore linked across the
// two representations only by that shared label — exactly what the canonical
// source key (name-else-id) collapses onto.
func seedNamedSourceChapters(ctx context.Context, t *testing.T, client *ent.Client, slug, provider, providerName string, importance, n int) []uuid.UUID {
	t.Helper()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetProviderName(providerName).SetImportance(importance).SaveX(ctx)
	return seedProviderChapters(ctx, t, client, s, sp, slug, provider, n)
}

// seedProviderChapters creates n wanted chapters (numbered 1..n, distinct keys and
// URLs) under the given series + provider row, returning their ids. Shared by the
// scanlator/named seed helpers so the ProviderChapter + Chapter creation lives in
// one place.
func seedProviderChapters(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series, sp *ent.SeriesProvider, slug, provider string, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, n)
	for i := range n {
		num := float64(i + 1)
		key := slug + "-" + itoa(i+1)
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(key).
			SetNillableNumber(&num).
			SetURL("https://" + provider + "/" + slug + "/" + itoa(i+1)).
			SetProviderIndex(i).
			SaveX(ctx)
		ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNillableNumber(&num).SaveX(ctx)
		ids = append(ids, ch.ID)
	}
	return ids
}

// TestRunOnce_MultiSeriesSharedSourceCap reproduces the production bug: THREE
// series all whose PRIMARY source is the SAME source ID, cap=5. At no instant may
// more than 5 of that source's chapters be in the downloading state.
func TestRunOnce_MultiSeriesSharedSourceCap(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		source = "7537715367149829912"
		cap    = 5
		perSer = 5
	)
	var all []uuid.UUID
	// Three DIFFERENT series, all primary source = the same source ID string.
	all = append(all, seedSourceChapters(ctx, t, client, "comix-A", source, 10, perSer)...)
	all = append(all, seedSourceChapters(ctx, t, client, "comix-B", source, 10, perSer)...)
	all = append(all, seedSourceChapters(ctx, t, client, "comix-C", source, 10, perSer)...)

	g := newGateFetcher() // gate starts closed: every fetch blocks
	d := download.New(client, g, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		&mutableSettings{conc: cap, retries: 3, backoff: time.Hour}, nil)

	done := make(chan error, 1)
	go func() { _, err := d.RunOnce(ctx); done <- err }()

	// Wait for the cap to be reached, then give any OVER-cap fetches a window to
	// start (they should not, if the cap holds).
	g.waitStarted(t, cap)
	time.Sleep(300 * time.Millisecond)

	counts := countStates(ctx, t, client, all)
	if got := counts[entchapter.StateDownloading]; got > cap {
		t.Errorf("BUG REPRODUCED: %d chapters downloading from one source, cap is %d", got, cap)
	} else {
		t.Logf("downloading=%d (cap=%d) — cap held", got, cap)
	}

	g.open()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// TestRunOnce_SamePhysicalSourceTwoProviderStrings REPRODUCES the production bug.
//
// The per-source cap is keyed on the SeriesProvider.provider STRING, both in
// groupBySource (the scheduler group key) and in providerLimiter (the fetch
// semaphore key). This codebase stores TWO different strings for the SAME physical
// source: the Suwayomi ingest path stores the numeric source id (ingest.go:261),
// while the disk-reconcile / library-import path stores the source NAME
// (reconcile.go:372). During a Kaizoku library migration BOTH paths are active, so
// one physical source ("Comix") ends up represented as e.g. "7537..." AND "Comix".
//
// Result: groupBySource makes TWO groups and newProviderLimiter makes TWO
// semaphores for the one physical source — each granted the FULL cap — so the
// source runs at 2x its per-source concurrency cap.
//
// The two representations are linked in production ONLY by the source display
// name: the Suwayomi row carries id="7537…" + provider_name="Comix", while the
// disk row carries provider="Comix" + no provider_name. The fix keys grouping +
// the fetch limiter on that shared label (name-else-id), collapsing both to one
// group / one cap.
func TestRunOnce_SamePhysicalSourceTwoProviderStrings(t *testing.T) {
	// Same physical Comix source, two representations of `provider`:
	//   - id "7537715367149829912" + name "Comix" (Suwayomi ingest: id in provider,
	//     resolved display name in provider_name).
	//   - provider "Comix", no provider_name (disk reconcile / library-import: the
	//     display name lands directly in provider).
	assertSharedPhysicalSourceCapHeld(t, "Comix")
}

// TestRunOnce_WhitespaceVariantProviderNameMergesCap reproduces the Kaizoku-import
// whitespace-mismatch variant of the shared-source bug: the disk provider string is
// parsed from ComicInfo Publisher / the filename bracket (disk/kaizoku.go), which can
// carry a trailing/leading space, so the disk row stores provider="Comix " while the
// Suwayomi row stores provider_name="Comix". Without trimming the canonical source
// key, "Comix " and "Comix" are two distinct keys → two groups / two semaphores →
// 2x the per-source cap. canonicalSourceKey's TrimSpace collapses them to one group,
// so the downloading-state count must stay ≤ cap. (Case is NOT folded — that is a
// separate over-merge decision, so this test only exercises whitespace.)
func TestRunOnce_WhitespaceVariantProviderNameMergesCap(t *testing.T) {
	// The disk row's provider carries a TRAILING SPACE ("Comix "), as a Kaizoku
	// import parsed from ComicInfo/filename can; it must still merge onto "Comix".
	assertSharedPhysicalSourceCapHeld(t, "Comix ")
}

// assertSharedPhysicalSourceCapHeld drives the shared-physical-source cap scenario
// and asserts the per-source cap holds. It seeds TWO SeriesProvider rows for the one
// physical Comix source — a Suwayomi row (id "7537…" + provider_name "Comix") and a
// disk row whose raw provider is diskProvider (e.g. "Comix" or the whitespace-variant
// "Comix ") — then runs one download cycle against a gate-closed fetcher (every fetch
// blocks, holding its slot) and asserts no more than cap of that source's chapters
// are in the downloading state at once. Keying by the raw provider string splits the
// source into two groups → two slot channels → 2x the cap; the canonical
// (trimmed, name-else-id) key collapses both to one group / one cap.
//
// Shared by the exact-name and whitespace-variant cases so the seed+run+assert lives
// in one place (§2 DRY) — the cases differ only in diskProvider.
func assertSharedPhysicalSourceCapHeld(t *testing.T, diskProvider string) {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)

	const cap = 5
	var all []uuid.UUID
	all = append(all, seedNamedSourceChapters(ctx, t, client, "comix-suwayomi", "7537715367149829912", "Comix", 10, cap)...)
	all = append(all, seedSourceChapters(ctx, t, client, "comix-disk", diskProvider, 5, cap)...)

	g := newGateFetcher() // gate closed: every fetch blocks, holding its slot
	d := download.New(client, g, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		&mutableSettings{conc: cap, retries: 3, backoff: time.Hour}, nil)

	done := make(chan error, 1)
	go func() { _, err := d.RunOnce(ctx); done <- err }()

	// A merged group fills exactly cap; a split (raw-string key) would let 2*cap start.
	g.waitStarted(t, cap)
	time.Sleep(300 * time.Millisecond)

	counts := countStates(ctx, t, client, all)
	got := counts[entchapter.StateDownloading]
	if got > cap {
		t.Errorf("BUG REPRODUCED (disk provider %q): %d chapters downloading from ONE physical source, per-source cap is %d (%.1fx)",
			diskProvider, got, cap, float64(got)/float64(cap))
	} else {
		t.Logf("downloading=%d (cap=%d) — disk provider %q merged, cap held", got, cap, diskProvider)
	}

	g.open()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// TestRunOnce_MultiScanlatorSharedSourceCap reproduces the scanlator-aware variant:
// ONE series with several (source,scanlator) providers all on the SAME source ID,
// each with wanted chapters. The per-source cap must still bound concurrent
// downloads from that physical source.
func TestRunOnce_MultiScanlatorSharedSourceCap(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		source = "7537715367149829912"
		cap    = 5
	)
	// Three (source,scanlator) providers on the same source, different series so
	// keys never collide, distinct importances so cands[0] is deterministic.
	var all []uuid.UUID
	all = append(all, seedSourceChaptersScanlator(ctx, t, client, "sc-A", source, "Reset Scans", 30, 5)...)
	all = append(all, seedSourceChaptersScanlator(ctx, t, client, "sc-B", source, "Asura", 20, 5)...)
	all = append(all, seedSourceChaptersScanlator(ctx, t, client, "sc-C", source, "ZScans", 10, 5)...)

	g := newGateFetcher()
	d := download.New(client, g, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		&mutableSettings{conc: cap, retries: 3, backoff: time.Hour}, nil)

	done := make(chan error, 1)
	go func() { _, err := d.RunOnce(ctx); done <- err }()

	g.waitStarted(t, cap)
	time.Sleep(300 * time.Millisecond)

	counts := countStates(ctx, t, client, all)
	if got := counts[entchapter.StateDownloading]; got > cap {
		t.Errorf("BUG REPRODUCED (scanlator): %d chapters downloading from one source, cap is %d", got, cap)
	} else {
		t.Logf("downloading=%d (cap=%d) — cap held", got, cap)
	}

	g.open()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}
