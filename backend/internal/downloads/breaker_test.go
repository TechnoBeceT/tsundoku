// Package downloads_test — the Slice-E1 visibility additions to the activity list:
// the circuit-breaker cooldown JOIN (a queued row explains a source-wide anti-ban
// cooldown, not only a persisted per-source backoff), the per-source N/max attempt
// badge, and the fresh-vs-upgrade marker. Each addition is proven correct AND proven
// to keep the list's query count bounded (no N+1).
package downloads_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// breakerStates is the filter the breaker-join assertions page over: the queued
// states a waiting reason is meaningful in.
var breakerStates = []entchapter.State{
	entchapter.StateWanted,
	entchapter.StateUpgradeAvailable,
	entchapter.StateUpgrading,
}

// newGate builds a REAL sourcegate.Service over the given client so the breaker join
// is exercised end-to-end — the same Snapshot() the production wiring passes, keyed
// by the same canonical source name. The thresholds are irrelevant to Snapshot (it
// only reads rows), so a zero-value Static suffices.
func newGate(client *ent.Client) *sourcegate.Service {
	return sourcegate.NewService(client, settings.Static{})
}

// seedBreakerSeries builds ONE series where each wanted chapter has its OWN single
// carrier source (so that source IS its primary/waited-on source), isolating each
// waiting case — the breaker is source-WIDE (keyed by name), so a shared primary
// would hit every chapter:
//
//   - b-1 → source "Cool Source": TRIPPED breaker (cooldown_until future), no
//     per-source next_attempt_at → cooling_down, retryAt=cooldown (the gap this closes).
//   - b-2 → source "Backoff Source": ONLY a future next_attempt_at, no breaker
//     → backoff, retryAt=next_attempt_at.
//   - b-3 → source "Healthy Source": neither signal → not waiting.
//   - b-4 → source "Both Source": BOTH signals, breaker LATER than the backoff
//     → cooling_down, retryAt=the later (breaker) time.
func seedBreakerSeries(ctx context.Context, t *testing.T, client *ent.Client) (breakerCool, breakerBoth time.Time) {
	t.Helper()
	cool := time.Now().Add(45 * time.Minute)      // breaker cooldown for b-1
	backoff := time.Now().Add(20 * time.Minute)   // per-source backoff for b-2 / b-4
	coolLater := time.Now().Add(90 * time.Minute) // b-4 breaker, later than its backoff

	s := client.Series.Create().SetTitle("Breaker").SetSlug("breaker").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	// One dedicated source per chapter so each chapter's primary is distinct.
	mkSource := func(id, name string) *ent.SeriesProvider {
		return client.SeriesProvider.Create().SetSeries(s).
			SetProvider(id).SetProviderName(name).SetImportance(10).SaveX(ctx)
	}
	mkFeed := func(sp *ent.SeriesProvider, key string) *ent.ProviderChapter {
		return client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(key).
			SetURL("https://src/" + key).SetProviderIndex(0).SaveX(ctx)
	}

	coolSrc := mkSource("cool", "Cool Source")
	backoffSrc := mkSource("backoff", "Backoff Source")
	healthySrc := mkSource("healthy", "Healthy Source")
	bothSrc := mkSource("both", "Both Source")

	mkFeed(coolSrc, "b-1")
	mkFeed(backoffSrc, "b-2").Update().SetNextAttemptAt(backoff).SetLastError("read tcp: connection reset").SaveX(ctx)
	mkFeed(healthySrc, "b-3")
	mkFeed(bothSrc, "b-4").Update().SetNextAttemptAt(backoff).SetLastError("backoff msg").SaveX(ctx)

	// Breakers are keyed by the canonical source NAME and block the WHOLE source (no
	// per-chapter next_attempt_at). Trip "Cool Source" and "Both Source".
	client.SourceCircuitState.Create().SetSourceKey("Cool Source").
		SetConsecutiveFailures(5).SetCooldownUntil(cool).
		SetLastError("Cloudflare challenge failed (403)").SaveX(ctx)
	client.SourceCircuitState.Create().SetSourceKey("Both Source").
		SetConsecutiveFailures(5).SetCooldownUntil(coolLater).
		SetLastError("blocked").SaveX(ctx)

	for _, key := range []string{"b-1", "b-2", "b-3", "b-4"} {
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetState(entchapter.StateWanted).SaveX(ctx)
	}
	return cool, coolLater
}

// TestListBreakerCooldownJoin proves the KNOWN GAP is closed: a wanted chapter held
// back purely by its source's tripped circuit-breaker (which writes cooldown_until in
// a DIFFERENT table, no per-source next_attempt_at) now surfaces
// waitingReason=cooling_down + the breaker's cooldown_until as the retry ETA, while a
// per-source backoff still reports "backoff" and a healthy source reports neither.
func TestListBreakerCooldownJoin(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	cool, coolLater := seedBreakerSeries(ctx, t, client)

	svc := downloads.NewService(client).WithBreakers(newGate(client))
	res, err := svc.List(ctx, downloads.ListFilter{States: breakerStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// b-1: breaker-only → cooling_down, ETA = the breaker cooldown, reason = breaker error.
	assertWaitingReason(t, res.Items, "b-1", "cooling_down", "Cloudflare challenge failed (403)")
	assertRetryAtNear(t, res.Items, "b-1", cool)
	// b-2: persisted backoff only, no breaker → backoff.
	assertWaitingReason(t, res.Items, "b-2", "backoff", "read tcp: connection reset")
	// b-3: no signal → not waiting.
	assertWaitingReason(t, res.Items, "b-3", "", "")
	// b-4: BOTH signals, breaker LATER → cooling_down wins, ETA = the later (breaker).
	assertWaitingReason(t, res.Items, "b-4", "cooling_down", "blocked")
	assertRetryAtNear(t, res.Items, "b-4", coolLater)
}

// mustItem returns the named chapter's DTO or fails the test.
func mustItem(t *testing.T, items []downloads.DownloadChapterDTO, key string) downloads.DownloadChapterDTO {
	t.Helper()
	item, ok := itemByKey(items, key)
	if !ok {
		t.Fatalf("chapter %s missing from the list", key)
	}
	return item
}

// assertWaitingReason fails unless the named chapter carries exactly wantReason +
// wantDetail, and a deferredUntil that is present iff the reason is non-empty.
func assertWaitingReason(t *testing.T, items []downloads.DownloadChapterDTO, key, wantReason, wantDetail string) {
	t.Helper()
	item := mustItem(t, items, key)
	if item.WaitingReason != wantReason {
		t.Errorf("%s waitingReason = %q, want %q", key, item.WaitingReason, wantReason)
	}
	if item.DeferReason != wantDetail {
		t.Errorf("%s deferReason = %q, want %q", key, item.DeferReason, wantDetail)
	}
	if hasETA, wantETA := item.DeferredUntil != nil, wantReason != ""; hasETA != wantETA {
		t.Errorf("%s deferredUntil present = %v, want %v", key, hasETA, wantETA)
	}
}

// assertRetryAtNear fails unless the named chapter's deferredUntil is within a second
// of want (robust to DB timestamp precision).
func assertRetryAtNear(t *testing.T, items []downloads.DownloadChapterDTO, key string, want time.Time) {
	t.Helper()
	item := mustItem(t, items, key)
	if item.DeferredUntil == nil || item.DeferredUntil.Sub(want).Abs() > time.Second {
		t.Errorf("%s deferredUntil = %v, want ~%v", key, item.DeferredUntil, want)
	}
}

// TestListBreakerJoin_NilGateFallsBack proves WithBreakers is optional: without a
// gate a breaker-tripped source shows NO cooling_down (falls back to backoff-only),
// so the ~20 existing NewService(client) call sites keep their exact behaviour.
func TestListBreakerJoin_NilGateFallsBack(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedBreakerSeries(ctx, t, client)

	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: breakerStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Breaker-only signal is invisible without a gate; the persisted backoff still surfaces.
	assertWaitingReason(t, res.Items, "b-1", "", "")
	assertWaitingReason(t, res.Items, "b-2", "backoff", "read tcp: connection reset")
}

// seedAttemptSeries builds one series covering the attempt-badge + isUpgrade cases:
//   - a-1 failed, no satisfier; carried ONLY by Comix (so Comix is the resolved
//     primary), its feed row attempts=2 → Attempts 2, not upgrade
//   - a-2 upgrade_available, satisfied by Comix, higher Asura carries it → isUpgrade true, target Asura
//   - a-3 downloaded, satisfied by Comix → isUpgrade false, no target
func seedAttemptSeries(ctx context.Context, t *testing.T, client *ent.Client) {
	t.Helper()
	s := client.Series.Create().SetTitle("Attempts").SetSlug("attempts").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)

	// a-1 is carried ONLY by Comix — an unsatisfied chapter resolves to its
	// highest-importance carrier, so a single carrier makes Comix unambiguously the
	// resolved source whose attempts the badge reads.
	a1PC := client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey("a-1").
		SetURL("https://comix/a-1").SetProviderIndex(0).SaveX(ctx)
	a1PC.Update().SetAttempts(2).SetLastError("timeout").SaveX(ctx)

	// a-2 / a-3 are carried by BOTH (Comix satisfier + higher Asura, the upgrade case).
	for i, key := range []string{"a-2", "a-3"} {
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).
			SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
			SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
	}

	client.Chapter.Create().SetSeries(s).SetChapterKey("a-1").SetState(entchapter.StateFailed).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("a-2").SetState(entchapter.StateUpgradeAvailable).
		SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).
		SetFilename("[Comix] Attempts a-2.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("a-3").SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).
		SetFilename("[Comix] Attempts a-3.cbz").SaveX(ctx)
}

// TestListAttemptsAndUpgradeMarker proves the N/max badge and the fresh-vs-upgrade
// marker: the resolved source's per-source attempt count populates Attempts, the live
// budget populates MaxRetries, and IsUpgrade tracks the upgrade STATE (distinct from
// whether a target can be named).
func TestListAttemptsAndUpgradeMarker(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedAttemptSeries(ctx, t, client)

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 3})
	res, err := svc.List(ctx, downloads.ListFilter{States: []entchapter.State{
		entchapter.StateFailed, entchapter.StateUpgradeAvailable, entchapter.StateDownloaded,
	}, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// a-1: fresh failed download, resolved to its sole carrier Comix, 2 attempts of 3.
	if a1 := mustItem(t, res.Items, "a-1"); a1.Attempts != 2 || a1.MaxRetries != 3 || a1.IsUpgrade || a1.ProviderName != "Comix" {
		t.Errorf("a-1 = attempts %d/max %d isUpgrade %v provider %q, want 2/3 false \"Comix\"",
			a1.Attempts, a1.MaxRetries, a1.IsUpgrade, a1.ProviderName)
	}
	// a-2: upgrade in flight — marker true AND a nameable target.
	if a2 := mustItem(t, res.Items, "a-2"); !a2.IsUpgrade || a2.UpgradeTarget != "Asura Scans" {
		t.Errorf("a-2 = isUpgrade %v target %q, want true \"Asura Scans\"", a2.IsUpgrade, a2.UpgradeTarget)
	}
	// a-3: downloaded — not an upgrade.
	if a3 := mustItem(t, res.Items, "a-3"); a3.IsUpgrade {
		t.Errorf("a-3 isUpgrade = true, want false (downloaded)")
	}
}

// TestListMaxRetries_NilPortDefaultsZero proves the retry-settings port is optional:
// without WithRetrySettings, MaxRetries reports 0 while the per-source attempt count
// still populates (so the ~20 existing NewService(client) call sites are unaffected).
func TestListMaxRetries_NilPortDefaultsZero(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedAttemptSeries(ctx, t, client)

	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: []entchapter.State{entchapter.StateFailed}, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if a1 := mustItem(t, res.Items, "a-1"); a1.MaxRetries != 0 || a1.Attempts != 2 {
		t.Errorf("a-1 without retry settings = attempts %d / max %d, want 2/0", a1.Attempts, a1.MaxRetries)
	}
}

// seedManyBreakerTripped creates one series whose PRIMARY source is breaker-tripped
// and n wanted chapters — so EVERY row on the page resolves a cooling_down join (the
// worst case for an N+1 the breaker snapshot could introduce).
func seedManyBreakerTripped(ctx context.Context, t *testing.T, client *ent.Client, n int) {
	t.Helper()
	s := client.Series.Create().SetTitle("Cool Wave").SetSlug("cool-wave").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	client.SourceCircuitState.Create().SetSourceKey("Asura Scans").
		SetConsecutiveFailures(5).SetCooldownUntil(time.Now().Add(time.Hour)).
		SetLastError("blocked").SaveX(ctx)
	for i := range n {
		key := fmt.Sprintf("c-%02d", i)
		client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
			SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetState(entchapter.StateWanted).SaveX(ctx)
	}
}

// TestListBreakerQueryCountIsPageSizeIndependent is the NO-N+1 proof WITH the breaker
// join added: the snapshot is loaded ONCE per List (not per row), so a page where
// every row resolves a cooling_down reason costs the SAME bounded query count at page
// size 2 and 20. An N+1 (one breaker lookup per row) would make the 20-row page cost
// ~18 more queries.
func TestListBreakerQueryCountIsPageSizeIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	seedManyBreakerTripped(ctx, t, seedClient, 20)

	client, drv := newCountingClient(db)
	// The gate reads through the SAME counted client, so its single Snapshot query is
	// included in the count.
	svc := downloads.NewService(client).WithBreakers(newGate(client)).WithRetrySettings(settings.Static{Retries: 3})

	count := func(limit int) int64 {
		drv.queries.Store(0)
		res, err := svc.List(ctx, downloads.ListFilter{States: breakerStates, Limit: limit})
		if err != nil {
			t.Fatalf("List(limit=%d): %v", limit, err)
		}
		if len(res.Items) != limit {
			t.Fatalf("List(limit=%d) returned %d items, want %d", limit, len(res.Items), limit)
		}
		for _, it := range res.Items {
			if it.WaitingReason != "cooling_down" {
				t.Fatalf("chapter %s waitingReason = %q, want cooling_down (every row is breaker-tripped)", it.ChapterKey, it.WaitingReason)
			}
		}
		return drv.queries.Load()
	}

	small, large := count(2), count(20)
	if small != large {
		t.Errorf("N+1: List issued %d queries for a 2-item page but %d for a 20-item page — the breaker join must not scale with page size", small, large)
	}
	// COUNT + chapters page (+ series/category eager loads) + ONE providers load
	// (+ feeds) + ONE breaker snapshot. A generous ceiling still fails an N+1.
	const maxQueries = 9
	if large > maxQueries {
		t.Errorf("List issued %d queries for one page, want <= %d (bounded, page-size independent)", large, maxQueries)
	}
	t.Logf("queries: page(2)=%d page(20)=%d", small, large)
}
