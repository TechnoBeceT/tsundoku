// Package downloads_test — the DEFERRAL surface of the activity list: a queued
// chapter whose source is under a persisted per-source cooldown must surface WHY it
// is not moving (deferredUntil + deferReason), while a ready one must not — and
// resolving it must not cost a single extra query (it reads from the feeds the list
// already batch-loads).
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
)

// queuedStates is the filter the deferral assertions page over: the queued states a
// deferral is meaningful in (wanted + the two upgrade states).
var queuedStates = []entchapter.State{
	entchapter.StateWanted,
	entchapter.StateUpgradeAvailable,
	entchapter.StateUpgrading,
}

// seedDeferralSeries builds ONE series with a low satisfier ("Comix", importance 5)
// and a high upgrade target ("Asura Scans", importance 10), both carrying every
// chapter, then puts specific target/primary feed rows under cooldown to cover each
// deferral case:
//
//   - d-1 upgrade_available, satisfied by Comix; TARGET (Asura) next_attempt_at in
//     the FUTURE + last_error   → deferredUntil set, deferReason surfaced
//   - d-2 upgrade_available, satisfied by Comix; TARGET next_attempt_at in the PAST
//     (+ a last_error)          → NOT deferred (ready next cycle), reason suppressed
//   - d-3 wanted, no satisfier; its PRIMARY source (Asura, importance 10) is under a
//     future cooldown + error   → deferredUntil set, deferReason surfaced
//   - d-4 wanted, no satisfier; no cooldown anywhere → NOT deferred
func seedDeferralSeries(ctx context.Context, t *testing.T, client *ent.Client) {
	t.Helper()
	future := time.Now().Add(30 * time.Minute)
	past := time.Now().Add(-30 * time.Minute)

	s := client.Series.Create().SetTitle("Deferral").SetSlug("deferral").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)

	keys := []string{"d-1", "d-2", "d-3", "d-4"}
	for i, key := range keys {
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).
			SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
	}
	// The high (target/primary) feed rows — captured so specific ones can be cooled.
	highPC := map[string]*ent.ProviderChapter{}
	for i, key := range keys {
		highPC[key] = client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
			SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
	}

	highPC["d-1"].Update().SetNextAttemptAt(future).SetLastError("Cloudflare challenge failed (403)").SaveX(ctx)
	highPC["d-2"].Update().SetNextAttemptAt(past).SetLastError("stale — already elapsed").SaveX(ctx)
	highPC["d-3"].Update().SetNextAttemptAt(future).SetLastError("read tcp: connection reset by peer").SaveX(ctx)
	// d-4: no cooldown written anywhere.

	upgrades := []string{"d-1", "d-2"}
	for _, key := range upgrades {
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetState(entchapter.StateUpgradeAvailable).
			SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).
			SetFilename("[Comix] Deferral " + key + ".cbz").SaveX(ctx)
	}
	for _, key := range []string{"d-3", "d-4"} {
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetState(entchapter.StateWanted).SaveX(ctx)
	}
}

// TestListDeferral proves a queued row answers WHY it is not moving: when the source
// the engine is waiting on (upgrade TARGET for an upgrading chapter, PRIMARY source
// for a wanted one) has a next_attempt_at in the future, the row carries
// deferredUntil + the source's last_error; a past/absent cooldown leaves both empty,
// so a ready row is never mislabelled as waiting. The waited-on source's NAME is the
// one already on the row (upgradeTarget for an upgrade, providerName for a wanted).
func TestListDeferral(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedDeferralSeries(ctx, t, client)

	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: queuedStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	assertDeferred(t, res.Items, "d-1", "Cloudflare challenge failed (403)")
	assertReady(t, res.Items, "d-2")
	assertDeferred(t, res.Items, "d-3", "read tcp: connection reset by peer")
	assertReady(t, res.Items, "d-4")

	// The waited-on source NAME is not duplicated onto a new field — it is the one the
	// row already shows: the upgradeTarget for an upgrade defer, providerName for a
	// wanted defer.
	if d1, _ := itemByKey(res.Items, "d-1"); d1.UpgradeTarget != "Asura Scans" {
		t.Errorf("chapter d-1 upgradeTarget = %q, want the waited-on target %q", d1.UpgradeTarget, "Asura Scans")
	}
	if d3, _ := itemByKey(res.Items, "d-3"); d3.ProviderName != "Asura Scans" {
		t.Errorf("chapter d-3 providerName = %q, want the waited-on primary %q", d3.ProviderName, "Asura Scans")
	}
}

// assertDeferred fails unless the named chapter carries a FUTURE deferredUntil and
// the expected deferReason (the waited-on source's last_error).
func assertDeferred(t *testing.T, items []downloads.DownloadChapterDTO, key, wantReason string) {
	t.Helper()
	item, ok := itemByKey(items, key)
	if !ok {
		t.Fatalf("chapter %s missing from the list", key)
	}
	if item.DeferredUntil == nil {
		t.Fatalf("chapter %s deferredUntil = nil, want a future timestamp", key)
	}
	if !item.DeferredUntil.After(time.Now()) {
		t.Errorf("chapter %s deferredUntil = %v, want a FUTURE time", key, item.DeferredUntil)
	}
	if item.DeferReason != wantReason {
		t.Errorf("chapter %s deferReason = %q, want %q", key, item.DeferReason, wantReason)
	}
}

// assertReady fails unless the named chapter carries NO deferral (nil deferredUntil,
// empty deferReason) — a source ready to try next cycle, never mislabelled as waiting.
func assertReady(t *testing.T, items []downloads.DownloadChapterDTO, key string) {
	t.Helper()
	item, ok := itemByKey(items, key)
	if !ok {
		t.Fatalf("chapter %s missing from the list", key)
	}
	if item.DeferredUntil != nil {
		t.Errorf("chapter %s deferredUntil = %v, want nil (source is ready next cycle)", key, item.DeferredUntil)
	}
	if item.DeferReason != "" {
		t.Errorf("chapter %s deferReason = %q, want %q (no defer ⇒ no reason)", key, item.DeferReason, "")
	}
}

// seedManyDeferredUpgrades creates one series with a low satisfier + a high upgrade
// target and n upgrade_available chapters whose TARGET feed row is under a future
// cooldown — so EVERY row on the page resolves a deferral (the worst case for an N+1
// introduced by the deferral read).
func seedManyDeferredUpgrades(ctx context.Context, t *testing.T, client *ent.Client, n int) {
	t.Helper()
	future := time.Now().Add(time.Hour)
	s := client.Series.Create().SetTitle("Deferred Wave").SetSlug("deferred-wave").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).SetProvider("high").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	for i := range n {
		key := fmt.Sprintf("d-%02d", i)
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).
			SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
			SetURL("https://asura/" + key).SetProviderIndex(i).
			SetNextAttemptAt(future).SetLastError("cooldown").SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).
			SetState(entchapter.StateUpgradeAvailable).
			SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).SaveX(ctx)
	}
}

// TestListDeferralQueryCountIsPageSizeIndependent is the NO-N+1 proof for the
// deferral fields: they are resolved IN MEMORY from the ProviderChapter feeds the
// list already batch-loads (waitedOnCarrier returns a feed row already in the index),
// so a page where every row is deferred costs the SAME bounded query count whether it
// holds 2 rows or 20. An N+1 (one cooldown lookup per row) would make the 20-row page
// cost ~18 more queries.
func TestListDeferralQueryCountIsPageSizeIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	seedManyDeferredUpgrades(ctx, t, seedClient, 20)

	client, drv := newCountingClient(db)
	svc := downloads.NewService(client)

	count := func(limit int) int64 {
		drv.queries.Store(0)
		res, err := svc.List(ctx, downloads.ListFilter{States: queuedStates, Limit: limit})
		if err != nil {
			t.Fatalf("List(limit=%d): %v", limit, err)
		}
		if len(res.Items) != limit {
			t.Fatalf("List(limit=%d) returned %d items, want %d", limit, len(res.Items), limit)
		}
		for _, it := range res.Items {
			if it.DeferredUntil == nil {
				t.Fatalf("chapter %s deferredUntil = nil, want set (every row is under cooldown)", it.ChapterKey)
			}
		}
		return drv.queries.Load()
	}

	small, large := count(2), count(20)
	if small != large {
		t.Errorf("N+1: List issued %d queries for a 2-item page but %d for a 20-item page — the deferral read must not scale with page size", small, large)
	}
	const maxQueries = 8
	if large > maxQueries {
		t.Errorf("List issued %d queries for one page, want <= %d (bounded, page-size independent)", large, maxQueries)
	}
	t.Logf("queries: page(2)=%d page(20)=%d", small, large)
}
