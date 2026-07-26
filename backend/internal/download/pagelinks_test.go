// Package download_test — the cached-page-link invalidation contract (GAP-119).
//
// ProviderChapter.page_links caches a chapter's resolved page list so a retry can
// skip the source's (often anti-bot-gated) page-resolution step. Some sources hand
// out TIME-LIMITED, SIGNED image URLs: once the signature lapses the cached list is
// dead, and before this contract existed every retry replayed the same dead URLs
// until the chapter burned its whole per-source budget on links that could never
// work again.
//
// The rule these tests pin: a failed attempt that RE-USED the cached links
// invalidates them (the next attempt re-resolves), while a failed attempt that
// resolved its links FRESH keeps them (that is a genuine source problem, and
// re-resolving would only cost an extra source call). The distinction can only be
// made from a flag captured BEFORE the fetch — the dispatcher write-throughs
// freshly-resolved links onto the same in-memory row mid-attempt, so afterwards
// both cases look identical.
//
// Requires Docker (via testcontainers).
package download_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// expiredLinks is the shape a source with signed, time-limited image URLs leaves
// behind on a ProviderChapter row: two pages whose signatures have since lapsed.
// The engine fake serves page 0 as a real image and page 1 as a truncated body,
// reproducing the observed symptom (the CDN cutting the response short) without
// needing a real expiry clock.
func expiredLinks() []fetcher.PageLink {
	return []fetcher.PageLink{
		{URL: "u0", ImageURL: "https://cdn.example/1.jpg?acc=sig&expires=1"},
		{URL: "u1", ImageURL: "https://cdn.example/2.jpg?acc=sig&expires=1"},
	}
}

// truncatingEngine builds an engine fake for sourceID that serves a fully valid
// first page and a TRUNCATED second page, so the real validating Fetcher fails the
// whole attempt with a broken-image error after staging page 0. Its page list is
// registered under chapterURL so a re-resolution (Client.Pages) also succeeds —
// which is what makes "was Pages called?" a meaningful assertion rather than an
// accident of the fixture.
func truncatingEngine(t *testing.T, sourceID int64, chapterURL string) *enginefake.Client {
	t.Helper()
	good := encodeTestJPEG(t)
	return enginefake.New(
		enginefake.WithPages(sourceID, chapterURL, []sourceengine.Page{
			{Index: 0, URL: "u0", ImageURL: "https://cdn.example/1.jpg?acc=fresh&expires=2"},
			{Index: 1, URL: "u1", ImageURL: "https://cdn.example/2.jpg?acc=fresh&expires=2"},
		}),
		enginefake.WithImage(sourceID, "u0", good, "image/jpeg"),
		enginefake.WithImage(sourceID, "u1", good[:12], "image/jpeg"), // valid magic, body cut short
	)
}

// TestDownload_CachedLinksFail_InvalidatesLinksAndStaging is the download-path
// proof of GAP-119: a chapter whose row already carries page links fetches from
// them (never calling Pages) and fails on a truncated page — so the links must be
// cleared, freeing the next attempt to re-resolve. The staged bytes must go with
// them: links and the index-keyed staging dir are invalidated as a PAIR, or a
// re-resolved (possibly reordered) list would be packed against stale files.
func TestDownload_CachedLinksFail_InvalidatesLinksAndStaging(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := mustTempDir(t)

	s := client.Series.Create().SetTitle("Signed URLs").SetSlug("signed-urls").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SetPageLinks(expiredLinks()).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	engineClient := truncatingEngine(t, 7, "/ch/c1")
	d := download.New(client, sourceengine.NewFetcher(engineClient, stagingRoot), sse.NewHub(),
		download.Config{Storage: mustTempDir(t), StagingRoot: stagingRoot},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n := engineClient.CallCount("Pages"); n != 0 {
		t.Fatalf("Pages called %d times, want 0 — the attempt must have run on the CACHED links for this test to mean anything", n)
	}
	if links := client.ProviderChapter.GetX(ctx, pc.ID).PageLinks; len(links) != 0 {
		t.Errorf("page_links = %d entries, want 0 — a failure on re-used links must invalidate them so the next attempt re-resolves", len(links))
	}
	stagingDir := filepath.Join(stagingRoot, pc.ID.String())
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir %s survived the invalidation (stat err = %v) — stale index-keyed pages must never outlive the links they came from", stagingDir, err)
	}
	// The failure classification itself is untouched: a broken page is
	// CHAPTER-SPECIFIC, so it still spends exactly one budget slot.
	if a := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; a != 1 {
		t.Errorf("attempts = %d, want 1 — link invalidation must not change how a failure is classified or charged", a)
	}
}

// TestDownload_FreshlyResolvedLinksFail_KeepsLinks is the other half of the rule:
// a chapter with NO stored links resolves them this attempt and then fails. Those
// links were minted seconds ago, so the failure is a genuine source problem — they
// must be KEPT (a re-resolve would be a wasted source call), and the staged pages
// must be kept too so the retry resumes.
//
// This is the guard against the tempting-but-wrong implementation that reads
// pc.PageLinks at failure time: the write-through has already populated the row by
// then, so such an implementation clears the cache on EVERY failure and this test
// is what catches it.
func TestDownload_FreshlyResolvedLinksFail_KeepsLinks(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := mustTempDir(t)

	s := client.Series.Create().SetTitle("Fresh Links").SetSlug("fresh-links").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	engineClient := truncatingEngine(t, 7, "/ch/c1")
	d := download.New(client, sourceengine.NewFetcher(engineClient, stagingRoot), sse.NewHub(),
		download.Config{Storage: mustTempDir(t), StagingRoot: stagingRoot},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n := engineClient.CallCount("Pages"); n != 1 {
		t.Fatalf("Pages called %d times, want 1 — this attempt must have resolved its own links for the test to mean anything", n)
	}
	if links := client.ProviderChapter.GetX(ctx, pc.ID).PageLinks; len(links) != 2 {
		t.Errorf("page_links = %d entries, want 2 — links resolved THIS attempt are not stale and must survive the failure", len(links))
	}
	stagingDir := filepath.Join(stagingRoot, pc.ID.String())
	if _, err := os.Stat(stagingDir); err != nil {
		t.Errorf("staging dir %s missing (stat err = %v) — a fresh-link failure must keep its staged pages so the retry resumes", stagingDir, err)
	}
}

// seedUpgradeTargetWithLinks seeds a chapter already downloaded from source "7"
// (importance 5) that source "8" (importance 10) also offers, so DetectUpgrades
// flags it and the upgrade path fetches from "8". Both provider rows carry stored
// page links — the satisfier's are there to prove invalidation touches ONLY the
// source that failed. targetLinks are the links on the upgrade target; pass nil to
// make the target resolve its list fresh.
func seedUpgradeTargetWithLinks(ctx context.Context, t *testing.T, client *ent.Client, targetLinks []fetcher.PageLink) (*ent.Chapter, *ent.ProviderChapter, *ent.ProviderChapter) {
	t.Helper()
	s := client.Series.Create().SetTitle("Upgrade Links").SetSlug("upgrade-links").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(5).SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("8").SetImportance(10).SaveX(ctx)

	lowPC := client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey("c1").
		SetURL("/low/c1").SetProviderIndex(0).SetPageLinks(expiredLinks()).SaveX(ctx)
	highCreate := client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey("c1").
		SetURL("/high/c1").SetProviderIndex(0)
	if len(targetLinks) > 0 {
		highCreate = highCreate.SetPageLinks(targetLinks)
	}
	highPC := highCreate.SaveX(ctx)

	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").
		SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(spLow.ID).SetSatisfiedImportance(5).
		SetFilename("[7] Upgrade Links 001.cbz").SetPageCount(2).SetDownloadDate(time.Now()).
		SaveX(ctx)
	return ch, lowPC, highPC
}

// runFailingUpgrade flags the chapter and runs one upgrade pass, asserting the
// upgrade was actually attempted and failed cleanly (the working copy is kept).
func runFailingUpgrade(ctx context.Context, t *testing.T, client *ent.Client, d *download.Dispatcher, ch *ent.Chapter) {
	t.Helper()
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 1 {
		t.Fatalf("DetectUpgrades flagged %d chapters, want 1", n)
	}
	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateDownloaded {
		t.Fatalf("state = %s, want downloaded (a failed upgrade keeps the working copy)", st)
	}
}

// TestUpgrade_CachedLinksFail_InvalidatesOnlyTheFailedSource carries GAP-119 onto
// the convergence-upgrade path, where the defect was actually observed: the
// upgrade target's links were written by an earlier failed DOWNLOAD attempt and
// can be arbitrarily old by the time an upgrade re-uses them. A failed upgrade on
// re-used links must invalidate that source's links — and only that source's, the
// current satisfier's cache is untouched.
func TestUpgrade_CachedLinksFail_InvalidatesOnlyTheFailedSource(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := mustTempDir(t)
	ch, lowPC, highPC := seedUpgradeTargetWithLinks(ctx, t, client, expiredLinks())

	engineClient := truncatingEngine(t, 8, "/high/c1")
	d := download.New(client, sourceengine.NewFetcher(engineClient, stagingRoot), sse.NewHub(),
		download.Config{Storage: mustTempDir(t), StagingRoot: stagingRoot},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	runFailingUpgrade(ctx, t, client, d, ch)

	if n := engineClient.CallCount("Pages"); n != 0 {
		t.Fatalf("Pages called %d times, want 0 — the upgrade must have run on the target's CACHED links", n)
	}
	if links := client.ProviderChapter.GetX(ctx, highPC.ID).PageLinks; len(links) != 0 {
		t.Errorf("upgrade target page_links = %d entries, want 0 — a failed upgrade on re-used links must invalidate them", len(links))
	}
	if links := client.ProviderChapter.GetX(ctx, lowPC.ID).PageLinks; len(links) != 2 {
		t.Errorf("satisfier page_links = %d entries, want 2 — only the source that failed may be invalidated", len(links))
	}
	stagingDir := filepath.Join(stagingRoot, highPC.ID.String())
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("upgrade staging dir %s survived (stat err = %v) — links and staged pages are invalidated together", stagingDir, err)
	}
	if a := client.ProviderChapter.GetX(ctx, highPC.ID).Attempts; a != 1 {
		t.Errorf("upgrade target attempts = %d, want 1 — invalidation must not change the classified charge", a)
	}
}

// TestUpgrade_FreshlyResolvedLinksFail_ClearsNothing mirrors the download-path
// fresh-link case on the upgrade path: an upgrade target with no stored links
// resolves its own list, fails, and must leave every row's cache exactly as it
// found it — the target still has none (the upgrade path deliberately does not
// write links through) and the satisfier's are untouched.
func TestUpgrade_FreshlyResolvedLinksFail_ClearsNothing(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := mustTempDir(t)
	ch, lowPC, highPC := seedUpgradeTargetWithLinks(ctx, t, client, nil)

	engineClient := truncatingEngine(t, 8, "/high/c1")
	d := download.New(client, sourceengine.NewFetcher(engineClient, stagingRoot), sse.NewHub(),
		download.Config{Storage: mustTempDir(t), StagingRoot: stagingRoot},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	runFailingUpgrade(ctx, t, client, d, ch)

	if n := engineClient.CallCount("Pages"); n != 1 {
		t.Fatalf("Pages called %d times, want 1 — the upgrade must have resolved its own links here", n)
	}
	if links := client.ProviderChapter.GetX(ctx, highPC.ID).PageLinks; len(links) != 0 {
		t.Errorf("upgrade target page_links = %d entries, want 0 — the upgrade path does not write links through", len(links))
	}
	if links := client.ProviderChapter.GetX(ctx, lowPC.ID).PageLinks; len(links) != 2 {
		t.Errorf("satisfier page_links = %d entries, want 2 — a fresh-link upgrade failure must clear nothing", len(links))
	}
}
