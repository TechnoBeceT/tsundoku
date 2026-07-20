// Package downloads_test — the HONEST FAILURES surface (PART D/E): the failures
// read-model surfaces not only state-failed chapters but ANY chapter with a
// chapter-specific per-source failure (ProviderChapter.attempts>0), including a
// DOWNLOADED chapter whose upgrade source keeps failing — naming the FAILING source,
// tagging it retryable vs terminal, and letting an owner retry reset that source.
package downloads_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
)

// failStates is the failures view's base state filter (state-failed chapters).
var failStates = []entchapter.State{entchapter.StateFailed, entchapter.StatePermanentlyFailed}

// seededFailure holds the ids a source-failure fixture targets.
type seededFailure struct {
	chID   uuid.UUID // downloaded chapter satisfied by low, with a failing high upgrade source
	lowID  uuid.UUID // "Comix" importance 5 — the satisfier
	highID uuid.UUID // "Asura Scans" importance 10 — the FAILING upgrade source
	highPC uuid.UUID // high's ProviderChapter feed row (attempts>0)
}

// seedDownloadedWithFailingUpgrade builds a DOWNLOADED chapter satisfied by a low
// source (importance 5) whose HIGHER source (importance 10) carries the same key with
// the given per-source failure state (attempts + last_error). The chapter is NOT in
// any failed state — it only reaches the failures view via IncludeSourceFailures.
func seedDownloadedWithFailingUpgrade(ctx context.Context, t *testing.T, client *ent.Client, highAttempts int, highErr string) seededFailure {
	t.Helper()
	s := client.Series.Create().SetTitle("Failing Upgrade").SetSlug("failing-upgrade").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)

	num := 1.0
	client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey("f-1").
		SetNillableNumber(&num).SetURL("https://comix/f-1").SetProviderIndex(0).SaveX(ctx)
	highPC := client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey("f-1").
		SetNillableNumber(&num).SetURL("https://asura/f-1").SetProviderIndex(0).
		SetAttempts(highAttempts).SetLastError(highErr).SetNextAttemptAt(time.Now().Add(30 * time.Minute)).SaveX(ctx)

	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("f-1").SetNillableNumber(&num).
		SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).
		SetFilename("[comix] Failing Upgrade 001.cbz").SetPageCount(10).SetDownloadDate(time.Now()).SaveX(ctx)

	return seededFailure{chID: ch.ID, lowID: low.ID, highID: high.ID, highPC: highPC.ID}
}

// TestListSourceFailures_DownloadedUpgradeSource proves PART D: a downloaded chapter
// whose upgrade source is failing (attempts>0) surfaces in the failures view (via
// IncludeSourceFailures), naming the FAILING source (not the satisfier), tagged as an
// upgrade converging to it, and classified retryable while it has budget left.
func TestListSourceFailures_DownloadedUpgradeSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedDownloadedWithFailingUpgrade(ctx, t, client, 2, "chapter not found") // 2 of 3 → retryable

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 3})
	res, err := svc.List(ctx, downloads.ListFilter{States: failStates, IncludeSourceFailures: true, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row, ok := itemByKey(res.Items, "f-1")
	if !ok {
		t.Fatal("downloaded chapter with a failing upgrade source missing from the failures view")
	}
	assertFailingSourceFields(t, row)
	// A DOWNLOADED chapter's broken upgrade source is retryable (2<3) and tagged as an
	// upgrade converging TO it.
	if !row.Retryable || row.Terminal {
		t.Errorf("retryable/terminal = %v/%v, want true/false (2 < 3)", row.Retryable, row.Terminal)
	}
	if !row.IsUpgrade || row.UpgradeTarget != "Asura Scans" {
		t.Errorf("isUpgrade/upgradeTarget = %v/%q, want true/Asura Scans (a broken upgrade fetch)", row.IsUpgrade, row.UpgradeTarget)
	}
}

// assertFailingSourceFields asserts the failing-source identity + N/max + category for
// the seeded downloaded-with-failing-upgrade fixture (2 of 3 attempts, not_found).
func assertFailingSourceFields(t *testing.T, row downloads.DownloadChapterDTO) {
	t.Helper()
	// The row still names the SUPPLIER (its satisfier) as `provider`…
	if row.Provider != "comix" {
		t.Errorf("provider = %q, want the satisfier %q (who supplies the chapter now)", row.Provider, "comix")
	}
	// …but the FAILING source is the higher upgrade target.
	if row.FailingProvider != "asura" || row.FailingProviderName != "Asura Scans" {
		t.Errorf("failing source = %q/%q, want asura/Asura Scans", row.FailingProvider, row.FailingProviderName)
	}
	if row.FailingAttempts != 2 || row.MaxRetries != 3 {
		t.Errorf("failing N/max = %d/%d, want 2/3", row.FailingAttempts, row.MaxRetries)
	}
	if row.FailingErrorCategory != "not_found" {
		t.Errorf("failingErrorCategory = %q, want not_found (derived from the message)", row.FailingErrorCategory)
	}
}

// TestListSourceFailures_Terminal proves the terminal classification: once the
// failing source has spent its whole budget (attempts >= maxRetries) the row is
// terminal, not retryable.
func TestListSourceFailures_Terminal(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedDownloadedWithFailingUpgrade(ctx, t, client, 3, "malformed body") // 3 of 3 → terminal

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 3})
	res, err := svc.List(ctx, downloads.ListFilter{States: failStates, IncludeSourceFailures: true, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row := mustItem(t, res.Items, "f-1")
	if row.FailingAttempts != 3 || row.Retryable || !row.Terminal {
		t.Errorf("f-1 = attempts %d retryable %v terminal %v, want 3/false/true", row.FailingAttempts, row.Retryable, row.Terminal)
	}
}

// TestListSourceFailures_ExcludedWithoutFlag proves the widening is OPT-IN: without
// IncludeSourceFailures, a downloaded chapter with a failing source stays out of the
// failed-states view (existing behaviour is byte-for-byte unchanged).
func TestListSourceFailures_ExcludedWithoutFlag(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedDownloadedWithFailingUpgrade(ctx, t, client, 2, "chapter not found")

	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: failStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := itemByKey(res.Items, "f-1"); ok {
		t.Error("f-1 appeared in the state-only failed view — the source-failure widening must be opt-in")
	}
	if res.Total != 0 {
		t.Errorf("total = %d, want 0 (no state-failed chapters seeded)", res.Total)
	}
}

// TestRetryChapter_DownloadedFailingUpgradeSource proves PART E: retrying a downloaded
// chapter whose upgrade source failed resets that source's ProviderChapter (attempts→0,
// last_error→"", next_attempt_at→null) WITHOUT changing the chapter's state, so
// DetectUpgrades re-flags the upgrade.
func TestRetryChapter_DownloadedFailingUpgradeSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedDownloadedWithFailingUpgrade(ctx, t, client, 2, "chapter not found")

	svc := downloads.NewService(client)
	if err := svc.RetryChapter(ctx, s.chID); err != nil {
		t.Fatalf("RetryChapter: %v", err)
	}
	// The failing source got a fresh budget.
	pc := client.ProviderChapter.GetX(ctx, s.highPC)
	if pc.Attempts != 0 || pc.LastError != "" || pc.NextAttemptAt != nil {
		t.Errorf("failing source not reset: attempts=%d lastErr=%q next=%v", pc.Attempts, pc.LastError, pc.NextAttemptAt)
	}
	// The chapter keeps its downloaded state + CBZ (never-auto-delete).
	if st := client.Chapter.GetX(ctx, s.chID).State; st != entchapter.StateDownloaded {
		t.Errorf("state = %s, want downloaded (a downloaded chapter's retry only re-arms its upgrade source)", st)
	}
}

// TestRetryAll_IncludeSourceFailures proves the bulk path resets a downloaded
// chapter's failing upgrade source too (keeping its state) when the flag is set, and
// counts it.
func TestRetryAll_IncludeSourceFailures(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedDownloadedWithFailingUpgrade(ctx, t, client, 2, "chapter not found")

	svc := downloads.NewService(client)
	n, err := svc.RetryAll(ctx, downloads.RetryAllFilter{IncludeSourceFailures: true})
	if err != nil {
		t.Fatalf("RetryAll: %v", err)
	}
	if n != 1 {
		t.Errorf("retried = %d, want 1 (the downloaded chapter's failing source)", n)
	}
	pc := client.ProviderChapter.GetX(ctx, s.highPC)
	if pc.Attempts != 0 || pc.NextAttemptAt != nil {
		t.Errorf("failing source not reset by RetryAll: attempts=%d next=%v", pc.Attempts, pc.NextAttemptAt)
	}
	if st := client.Chapter.GetX(ctx, s.chID).State; st != entchapter.StateDownloaded {
		t.Errorf("state = %s, want downloaded (RetryAll must not move a source-failing downloaded chapter)", st)
	}
}

// seedManyDownloadedFailing creates one series with n downloaded chapters, each
// satisfied by a low source and each with a HIGHER failing upgrade source (attempts>0)
// — so EVERY row on the page is surfaced via the source-failure widening AND needs the
// failing-source resolution (the worst case for an N+1).
func seedManyDownloadedFailing(ctx context.Context, t *testing.T, client *ent.Client, n int) {
	t.Helper()
	s := client.Series.Create().SetTitle("Fail Wave").SetSlug("fail-wave").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).SetProvider("high").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	for i := range n {
		key := chapterKey(i)
		num := float64(i)
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).
			SetNillableNumber(&num).SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
			SetNillableNumber(&num).SetURL("https://asura/" + key).SetProviderIndex(i).
			SetAttempts(2).SetLastError("chapter not found").SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNillableNumber(&num).
			SetState(entchapter.StateDownloaded).
			SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).
			SetFilename("[comix] " + key + ".cbz").SaveX(ctx)
	}
}

// chapterKey formats a stable zero-padded key for the wave seeds.
func chapterKey(i int) string {
	return fmt.Sprintf("fw-%03d", i)
}

// TestListSourceFailures_QueryCountIsPageSizeIndependent is the NO-N+1 proof for the
// source-failure widening: the correlated EXISTS predicate keeps the count + page
// queries at a constant number, and the failing-source resolution is in memory over
// the already-batch-loaded feeds — so a page where every row is a source-failure costs
// the SAME bounded query count at page size 2 and 20.
func TestListSourceFailures_QueryCountIsPageSizeIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	seedManyDownloadedFailing(ctx, t, seedClient, 20)

	client, drv := newCountingClient(db)
	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 3})

	count := func(limit int) int64 {
		drv.queries.Store(0)
		res, err := svc.List(ctx, downloads.ListFilter{States: failStates, IncludeSourceFailures: true, Limit: limit})
		if err != nil {
			t.Fatalf("List(limit=%d): %v", limit, err)
		}
		if len(res.Items) != limit {
			t.Fatalf("List(limit=%d) returned %d items, want %d", limit, len(res.Items), limit)
		}
		for _, it := range res.Items {
			if it.FailingProvider != "high" {
				t.Fatalf("chapter %s failingProvider = %q, want high (every row is a source-failure)", it.ChapterKey, it.FailingProvider)
			}
		}
		return drv.queries.Load()
	}

	small, large := count(2), count(20)
	if small != large {
		t.Errorf("N+1: List issued %d queries for a 2-item page but %d for a 20-item page — the source-failure widening must not scale with page size", small, large)
	}
	t.Logf("queries: page(2)=%d page(20)=%d", small, large)
}
