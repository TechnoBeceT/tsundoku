// Package downloads_test exercises the cross-library download-activity service
// against an ephemeral PostgreSQL instance (testdb). Tests require Docker.
package downloads_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// catID resolves a seeded default category's id by name (testdb seeds them).
func catID(ctx context.Context, db *ent.Client, name string) uuid.UUID {
	id, err := category.IDByName(ctx, db, name)
	if err != nil {
		panic(err)
	}
	return id
}

// seeded holds the ids the assertions target.
type seeded struct {
	alphaID  uuid.UUID
	betaID   uuid.UUID
	provHigh uuid.UUID // alpha "mangadex" importance 10 (has cover + title)
	provLow  uuid.UUID // alpha "asura" importance 5
	chFailed uuid.UUID // alpha-1, failed, satisfied_by provLow
	chWanted uuid.UUID // alpha-2, wanted, no satisfied_by
	chDone   uuid.UUID // alpha-3, downloaded
	chPerm   uuid.UUID // beta-1, permanently_failed, nil number/page/date
}

// seedLibrary builds two series spanning the enrichment + state surface:
//
//   - "Alpha Saga" (Manga): two providers — mangadex (importance 10, with a
//     per-source title + cover) and asura (importance 5). Three chapters: a-1
//     failed (satisfied_by asura, full failure bookkeeping), a-2 wanted (no
//     satisfied source), a-3 downloaded (filename/pages/date set).
//   - "Beta Quest" (Manhwa): one provider flame (no cover). One chapter b-1
//     permanently_failed with nil number/page_count/download_date.
func seedLibrary(ctx context.Context, t *testing.T, client *ent.Client) seeded {
	t.Helper()

	// cover_version is the hash of the CACHED cover's BYTES — set here so alpha has
	// a cover on disk and its proxy path is emitted VERSIONED ("…/cover?v=<version>").
	// A series with no cached cover carries no version and no "?v=" (see beta).
	alpha := client.Series.Create().
		SetTitle("Alpha Saga").SetSlug("alpha-saga").
		SetCoverVersion("a1b2c3d4e5f6").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	beta := client.Series.Create().
		SetTitle("Beta Quest").SetSlug("beta-quest").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)

	// provHigh carries a provider_name ("MangaDex") so the DTO shows the display
	// label; provLow has none so its label falls back to the raw provider id.
	provHigh := client.SeriesProvider.Create().
		SetSeriesID(alpha.ID).SetProvider("mangadex").SetProviderName("MangaDex").SetLanguage("en").
		SetImportance(10).SetTitle("Alpha Saga (MangaDex)").
		SetCoverURL("/cover/alpha-high.jpg").SaveX(ctx)
	provLow := client.SeriesProvider.Create().
		SetSeriesID(alpha.ID).SetProvider("asura").SetLanguage("en").
		SetImportance(5).SaveX(ctx)
	client.SeriesProvider.Create().
		SetSeriesID(beta.ID).SetProvider("flame").SetLanguage("en").
		SetImportance(1).SaveX(ctx)

	// Provider chapter feeds — a-2 titled by BOTH sources so the best-provider
	// (highest importance) name wins over the lower one.
	client.ProviderChapter.Create().
		SetSeriesProviderID(provHigh.ID).SetChapterKey("a-2").SetName("The Beginning").SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(provLow.ID).SetChapterKey("a-2").SetName("lower-priority name").SaveX(ctx)

	next := time.Now().UTC().Add(30 * time.Minute)
	chFailed := client.Chapter.Create().
		SetSeriesID(alpha.ID).SetChapterKey("a-1").SetNumber(1).
		SetState(entchapter.StateFailed).
		SetSatisfiedByProviderID(provLow.ID).
		SetRetries(2).SetLastError("connection reset").SetErrorCategory("network").
		SetNextAttemptAt(next).SaveX(ctx)
	chWanted := client.Chapter.Create().
		SetSeriesID(alpha.ID).SetChapterKey("a-2").SetNumber(2).
		SetState(entchapter.StateWanted).SaveX(ctx)
	chDone := client.Chapter.Create().
		SetSeriesID(alpha.ID).SetChapterKey("a-3").SetNumber(3).
		SetState(entchapter.StateDownloaded).
		SetFilename("[mangadex][en] Alpha Saga 003.cbz").
		SetPageCount(20).SetDownloadDate(time.Now().UTC()).SaveX(ctx)
	chPerm := client.Chapter.Create().
		SetSeriesID(beta.ID).SetChapterKey("b-1").
		SetState(entchapter.StatePermanentlyFailed).SaveX(ctx)

	return seeded{
		alphaID: alpha.ID, betaID: beta.ID,
		provHigh: provHigh.ID, provLow: provLow.ID,
		chFailed: chFailed.ID, chWanted: chWanted.ID, chDone: chDone.ID, chPerm: chPerm.ID,
	}
}

// itemByKey finds the listed item with the given chapter key.
func itemByKey(items []downloads.DownloadChapterDTO, key string) (downloads.DownloadChapterDTO, bool) {
	for _, it := range items {
		if it.ChapterKey == key {
			return it, true
		}
	}
	return downloads.DownloadChapterDTO{}, false
}

func TestListStateFilterSingle(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	got, err := svc.List(ctx, downloads.ListFilter{States: []entchapter.State{entchapter.StateFailed}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 {
		t.Fatalf("want total=1/items=1, got total=%d items=%d", got.Total, len(got.Items))
	}
	if got.Items[0].ID != s.chFailed {
		t.Errorf("want chFailed, got %s", got.Items[0].ID)
	}
}

func TestListStateFilterCSVOR(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	got, err := svc.List(ctx, downloads.ListFilter{
		States: []entchapter.State{entchapter.StateFailed, entchapter.StatePermanentlyFailed},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Total != 2 || len(got.Items) != 2 {
		t.Fatalf("want total=2/items=2, got total=%d items=%d", got.Total, len(got.Items))
	}
	if _, ok := itemByKey(got.Items, "a-1"); !ok {
		t.Error("missing a-1")
	}
	if _, ok := itemByKey(got.Items, "b-1"); !ok {
		t.Error("missing b-1")
	}
}

func TestListPaginationTotalVsPage(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	all := []entchapter.State{
		entchapter.StateFailed, entchapter.StatePermanentlyFailed,
		entchapter.StateWanted, entchapter.StateDownloaded,
	}
	page1, err := svc.List(ctx, downloads.ListFilter{States: all, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if page1.Total != 4 {
		t.Errorf("want total=4, got %d", page1.Total)
	}
	if len(page1.Items) != 2 {
		t.Errorf("want 2 items on page1, got %d", len(page1.Items))
	}
	page2, err := svc.List(ctx, downloads.ListFilter{States: all, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if page2.Total != 4 || len(page2.Items) != 2 {
		t.Errorf("want total=4/items=2 on page2, got total=%d items=%d", page2.Total, len(page2.Items))
	}
	// The two pages must be disjoint (stable order by number then key).
	for _, a := range page1.Items {
		if _, dup := itemByKey(page2.Items, a.ChapterKey); dup {
			t.Errorf("page1 and page2 overlap on %s", a.ChapterKey)
		}
	}
}

func TestListQueryTitleFilter(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	all := []entchapter.State{
		entchapter.StateFailed, entchapter.StatePermanentlyFailed,
		entchapter.StateWanted, entchapter.StateDownloaded,
	}
	// "beta" (lower-case) must match "Beta Quest" case-insensitively → only b-1.
	got, err := svc.List(ctx, downloads.ListFilter{States: all, Query: "beta"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 {
		t.Fatalf("want total=1/items=1, got total=%d items=%d", got.Total, len(got.Items))
	}
	if got.Items[0].ChapterKey != "b-1" {
		t.Errorf("want b-1, got %s", got.Items[0].ChapterKey)
	}
}

// TestListEnrichmentMultiSeries asserts that across a TWO-series page every
// enriched field resolves correctly from the single batched provider load
// (the no-N+1 path): chapter name from the best provider, provider from
// satisfied_by else best, the resolved display title + cover proxy path, and the
// nullable fields round-tripping as nil.
func TestListEnrichmentMultiSeries(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	all := []entchapter.State{
		entchapter.StateFailed, entchapter.StatePermanentlyFailed,
		entchapter.StateWanted, entchapter.StateDownloaded,
	}
	got, err := svc.List(ctx, downloads.ListFilter{States: all})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertWantedEnrichment(t, got.Items, s.alphaID)
	assertSatisfiedByProvider(t, got.Items)
	assertNoCoverAndNullables(t, got.Items)
}

// assertWantedEnrichment checks a-2 (wanted, no satisfied source): the name comes
// from the HIGHEST-importance provider's feed, the provider is the highest-importance
// source whose feed CARRIES a-2 (both do here, so mangadex wins), and the display
// title + cover proxy path resolve from the metadata source.
func assertWantedEnrichment(t *testing.T, items []downloads.DownloadChapterDTO, alphaID uuid.UUID) {
	t.Helper()
	a2, ok := itemByKey(items, "a-2")
	if !ok {
		t.Fatal("missing a-2")
	}
	if a2.Name != "The Beginning" {
		t.Errorf("a-2 name: want best-provider 'The Beginning', got %q", a2.Name)
	}
	if a2.Provider != "mangadex" {
		t.Errorf("a-2 provider: want feed-carrying 'mangadex', got %q", a2.Provider)
	}
	if a2.ProviderName != "MangaDex" {
		t.Errorf("a-2 providerName: want display 'MangaDex', got %q", a2.ProviderName)
	}
	if a2.SeriesTitle != "Alpha Saga (MangaDex)" {
		t.Errorf("a-2 seriesTitle: want resolved display 'Alpha Saga (MangaDex)', got %q", a2.SeriesTitle)
	}
	// The cover path is VERSIONED (…/cover?v=<hash of the cached cover's BYTES>) so
	// it can be served immutably — the version changes whenever the image changes.
	// The downloads DTO reuses the SAME resolver as series detail (§2 DRY), so it
	// must emit the identical versioned path. A series with no cached cover has no
	// version and therefore no "?v=" at all.
	wantCover := "/api/series/" + alphaID.String() + "/cover?v="
	if !strings.HasPrefix(a2.SeriesCoverURL, wantCover) {
		t.Errorf("a-2 coverUrl: want prefix %q, got %q", wantCover, a2.SeriesCoverURL)
	}
	if a2.SeriesCategory != "Manga" {
		t.Errorf("a-2 category: want Manga, got %q", a2.SeriesCategory)
	}
}

// assertSatisfiedByProvider checks a-1 (failed, satisfied_by asura): the provider
// is the satisfying source, and the failure bookkeeping is surfaced.
func assertSatisfiedByProvider(t *testing.T, items []downloads.DownloadChapterDTO) {
	t.Helper()
	a1, ok := itemByKey(items, "a-1")
	if !ok {
		t.Fatal("missing a-1")
	}
	if a1.Provider != "asura" {
		t.Errorf("a-1 provider: want satisfied-by 'asura', got %q", a1.Provider)
	}
	if a1.ProviderName != "asura" {
		t.Errorf("a-1 providerName: want id fallback 'asura' (no provider_name), got %q", a1.ProviderName)
	}
	if a1.Retries != 2 || a1.LastError != "connection reset" || a1.ErrorCategory != "network" {
		t.Errorf("a-1 failure fields not surfaced: %+v", a1)
	}
	if a1.NextAttemptAt == nil {
		t.Error("a-1 nextAttemptAt should be set")
	}
}

// assertNoCoverAndNullables checks b-1 (permanently_failed): a series with no cover
// yields an empty coverUrl, nil number/pageCount/downloadDate round-trip as nil, and
// — because NO provider feed carries b-1 — the row names NO source at all. Naming
// the series' only source would claim it is fetching a chapter it does not offer.
func assertNoCoverAndNullables(t *testing.T, items []downloads.DownloadChapterDTO) {
	t.Helper()
	b1, ok := itemByKey(items, "b-1")
	if !ok {
		t.Fatal("missing b-1")
	}
	if b1.SeriesCoverURL != "" {
		t.Errorf("b-1 coverUrl: want empty (no cover), got %q", b1.SeriesCoverURL)
	}
	if b1.Number != nil || b1.PageCount != nil || b1.DownloadDate != nil {
		t.Errorf("b-1 nullable fields should be nil, got number=%v pageCount=%v downloadDate=%v",
			b1.Number, b1.PageCount, b1.DownloadDate)
	}
	if b1.Provider != "" {
		t.Errorf("b-1 provider: want '' (no feed carries b-1), got %q", b1.Provider)
	}
}

func TestRetryChapterResetsFailed(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	if err := svc.RetryChapter(ctx, s.chFailed); err != nil {
		t.Fatalf("RetryChapter: %v", err)
	}
	ch := client.Chapter.GetX(ctx, s.chFailed)
	if ch.State != entchapter.StateWanted {
		t.Errorf("state: want wanted, got %s", ch.State)
	}
	if ch.Retries != 0 || ch.LastError != "" || ch.ErrorCategory != "" || ch.NextAttemptAt != nil {
		t.Errorf("failure fields not cleared: retries=%d lastErr=%q cat=%q next=%v",
			ch.Retries, ch.LastError, ch.ErrorCategory, ch.NextAttemptAt)
	}
}

func TestRetryChapterPermanentlyFailedEscape(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	if err := svc.RetryChapter(ctx, s.chPerm); err != nil {
		t.Fatalf("RetryChapter (permanently_failed): %v", err)
	}
	if client.Chapter.GetX(ctx, s.chPerm).State != entchapter.StateWanted {
		t.Error("permanently_failed chapter was not reset to wanted")
	}
}

func TestRetryChapterNotRetryable(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	for name, id := range map[string]uuid.UUID{"wanted": s.chWanted, "downloaded": s.chDone} {
		t.Run(name, func(t *testing.T) {
			err := svc.RetryChapter(ctx, id)
			if !errors.Is(err, downloads.ErrNotRetryable) {
				t.Fatalf("want ErrNotRetryable, got %v", err)
			}
			// The state must be untouched.
			if client.Chapter.GetX(ctx, id).State == entchapter.StateWanted && name != "wanted" {
				t.Error("state changed on a rejected retry")
			}
		})
	}
}

func TestRetryChapterNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	if err := svc.RetryChapter(ctx, uuid.New()); !errors.Is(err, downloads.ErrChapterNotFound) {
		t.Fatalf("want ErrChapterNotFound, got %v", err)
	}
}

func TestRetryAllDefaultResetsRetryable(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	n, err := svc.RetryAll(ctx, downloads.RetryAllFilter{})
	if err != nil {
		t.Fatalf("RetryAll: %v", err)
	}
	if n != 2 {
		t.Errorf("want retried=2 (failed + permanently_failed), got %d", n)
	}
	if client.Chapter.GetX(ctx, s.chFailed).State != entchapter.StateWanted ||
		client.Chapter.GetX(ctx, s.chPerm).State != entchapter.StateWanted {
		t.Error("both retryable chapters should be wanted")
	}
	// Non-retryable chapters are untouched.
	if client.Chapter.GetX(ctx, s.chDone).State != entchapter.StateDownloaded {
		t.Error("downloaded chapter must not be reset")
	}
}

func TestRetryAllSeriesScope(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	n, err := svc.RetryAll(ctx, downloads.RetryAllFilter{SeriesID: &s.alphaID})
	if err != nil {
		t.Fatalf("RetryAll: %v", err)
	}
	if n != 1 {
		t.Errorf("want retried=1 (only alpha's failed), got %d", n)
	}
	if client.Chapter.GetX(ctx, s.chPerm).State != entchapter.StatePermanentlyFailed {
		t.Error("beta's permanently_failed chapter must be untouched (out of scope)")
	}
}

func TestRetryAllExplicitState(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	s := seedLibrary(ctx, t, client)
	svc := downloads.NewService(client)

	n, err := svc.RetryAll(ctx, downloads.RetryAllFilter{
		States: []entchapter.State{entchapter.StateFailed},
	})
	if err != nil {
		t.Fatalf("RetryAll: %v", err)
	}
	if n != 1 {
		t.Errorf("want retried=1 (failed only), got %d", n)
	}
	if client.Chapter.GetX(ctx, s.chPerm).State != entchapter.StatePermanentlyFailed {
		t.Error("permanently_failed must be untouched when state=failed only")
	}
}

// seedExhaustedSource creates a series with a failed chapter whose single source
// has spent per-source retry state (attempts + last_error + a future cooldown), so
// a retry-reset test can assert the ProviderChapter row is reset too. Returns the
// chapter id and the ProviderChapter id.
func seedExhaustedSource(ctx context.Context, t *testing.T, client *ent.Client) (chID, pcID uuid.UUID) {
	t.Helper()
	s := client.Series.Create().SetTitle("Exhausted").SetSlug("exhausted-src").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("only").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("x-1").SetProviderIndex(0).
		SetAttempts(3).SetLastError("boom").SetNextAttemptAt(time.Now().Add(time.Hour)).
		SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("x-1").SetNumber(1).
		SetState(entchapter.StatePermanentlyFailed).SaveX(ctx)
	return ch.ID, pc.ID
}

// TestRetryChapterResetsProviderSources verifies that RetryChapter resets the
// per-source retry state on the chapter's ProviderChapter rows (attempts→0,
// last_error→"", next_attempt_at→null), giving every source a fresh budget — not
// just the Chapter row.
func TestRetryChapterResetsProviderSources(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	chID, pcID := seedExhaustedSource(ctx, t, client)
	svc := downloads.NewService(client)

	if err := svc.RetryChapter(ctx, chID); err != nil {
		t.Fatalf("RetryChapter: %v", err)
	}
	pc := client.ProviderChapter.GetX(ctx, pcID)
	if pc.Attempts != 0 || pc.LastError != "" || pc.NextAttemptAt != nil {
		t.Errorf("source retry state not reset: attempts=%d lastErr=%q next=%v",
			pc.Attempts, pc.LastError, pc.NextAttemptAt)
	}
}

// TestRetryAllResetsProviderSources verifies the same per-source reset for the
// bulk RetryAll path.
func TestRetryAllResetsProviderSources(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	_, pcID := seedExhaustedSource(ctx, t, client)
	svc := downloads.NewService(client)

	if _, err := svc.RetryAll(ctx, downloads.RetryAllFilter{}); err != nil {
		t.Fatalf("RetryAll: %v", err)
	}
	pc := client.ProviderChapter.GetX(ctx, pcID)
	if pc.Attempts != 0 || pc.LastError != "" || pc.NextAttemptAt != nil {
		t.Errorf("source retry state not reset by RetryAll: attempts=%d lastErr=%q next=%v",
			pc.Attempts, pc.LastError, pc.NextAttemptAt)
	}
}
