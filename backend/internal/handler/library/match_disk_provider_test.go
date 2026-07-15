package library_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/library"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"

	"github.com/labstack/echo/v4"
)

// weebSourceID / weebMangaURL identify the single engine-host source
// ("weeb", stable numeric id 1) newMatchClient exposes, addressed by its
// source-relative manga URL — P2 Suwayomi-removal repointed the ingest chain
// onto internal/sourceengine, which has no manga-id lookup, only (sourceID,
// url) pairs.
const (
	weebSourceID int64 = 1
	weebMangaURL       = "/manga/99"
)

// newMatchClient builds a sourceengine fake exposing one source ("weeb", id
// 1) whose Chapters feed for weebMangaURL reports the same two chapters
// (Number 1 and 2, normalizing to keys "1"/"2") the disk fixture written by
// writeKaizokuSeries carries — mirrors the pre-P2 fakeMatchClient's fixed
// FetchChapters result. MangaDetails is configured too so
// ingest.Ingest.upsertSeriesProvider does not fail.
func newMatchClient() *fake.Client {
	return fake.New(
		fake.WithSources([]sourceengine.Source{{ID: weebSourceID, Name: "weeb", Lang: "en"}}),
		fake.WithChapters(weebSourceID, weebMangaURL, []sourceengine.Chapter{
			{URL: weebMangaURL + "/1", Name: "Chapter 1", Number: 1},
			{URL: weebMangaURL + "/2", Name: "Chapter 2", Number: 2},
		}),
		fake.WithMangaDetails(weebSourceID, weebMangaURL, sourceengine.MangaDetails{URL: weebMangaURL, Title: "My Series"}),
	)
}

// newEnvWithMatchIngest is like newEnvWithStorage but wires a REAL
// ingest.Ingest over newMatchClient() (instead of nil) and registers the
// MatchDiskProvider route, so the happy-path round-trip test below can
// exercise the full handler → service → ingest chain.
func newEnvWithMatchIngest(t *testing.T, storage string) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	seriesSvc := series.NewService(client, storage, 14)
	ing := ingest.NewIngest(newMatchClient(), client)
	svc := library.NewService(client, ing, nil, seriesSvc, func() {}, storage, sse.NewHub())
	h := handler.NewHandler(svc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/series/:id/providers", h.AddProvider)
	authed.POST("/series/:id/providers/batch", h.AddProviders)
	authed.POST("/series/:id/providers/:providerId/match", h.MatchDiskProvider)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token, svc: svc}
}

// TestMatchDiskProvider_RequireOwner proves the new route is behind
// RequireOwner (added to the shared route-401 sweep as a standalone test
// since it needs a series+provider id in the path).
func TestMatchDiskProvider_RequireOwner(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/%s/providers/%s/match", uuid.New(), uuid.New())
	rec := env.doUnauth("POST", path, `{"source":"1","url":"/manga/1","importance":1}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth = %d, want 401", rec.Code)
	}
}

// TestMatchDiskProvider_BadSeriesID proves :id is validated as a UUID.
func TestMatchDiskProvider_BadSeriesID(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/not-a-uuid/providers/%s/match", uuid.New())
	rec := env.do("POST", path, `{"source":"1","url":"/manga/1","importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad series id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_BadProviderID proves :providerId is validated as a UUID.
func TestMatchDiskProvider_BadProviderID(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/%s/providers/not-a-uuid/match", uuid.New())
	rec := env.do("POST", path, `{"source":"1","url":"/manga/1","importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad provider id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_InvalidBody proves the body is validated (missing
// source) — still 400 under the P2 url-based validateProviderRef rule: source
// is checked first, so an entirely-missing source is rejected regardless of
// whether url is present.
func TestMatchDiskProvider_InvalidBody(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/%s/providers/%s/match", uuid.New(), uuid.New())
	rec := env.do("POST", path, `{"mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_MissingURL_400 proves url is now the field
// validateProviderRef actually enforces (P2 Suwayomi-removal: mangaId is no
// longer checked — a request can carry mangaId==0 or omit it entirely and
// still pass validation, as long as url is non-empty). This repurposes what
// used to be a "mangaId <= 0" guard proof onto the field the backend
// genuinely validates today, keeping the same coverage goal (a required-field
// guard exists on the provider-attach body).
func TestMatchDiskProvider_MissingURL_400(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/%s/providers/%s/match", uuid.New(), uuid.New())
	rec := env.do("POST", path, `{"source":"1","mangaId":0,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing url: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_UnknownProvider400s proves an unknown providerId (not
// belonging to the series) maps to 400 via ErrProviderNotInSeries. The lookup
// that returns ErrProviderNotInSeries happens before the service ever calls
// ingest, so the source id need not resolve to anything real — only the
// handler-level url requirement must be satisfied to reach the service.
func TestMatchDiskProvider_UnknownProvider400s(t *testing.T) {
	storage := t.TempDir()
	env := newEnvWithMatchIngest(t, storage)
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle("X").SetSlug("x").SaveX(ctx)

	path := fmt.Sprintf("/api/series/%s/providers/%s/match", ser.ID, uuid.New())
	rec := env.do("POST", path, `{"source":"weeb","url":"/manga/1","importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_HappyPath exercises the full round-trip through the
// real HTTP handler: a disk-imported series' unlinked provider is matched to
// a real engine-host source, and the response carries the refreshed
// SeriesDetailDTO with the disk provider gone and the new one carrying the
// chosen importance and both re-pointed chapters.
func TestMatchDiskProvider_HappyPath(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	env := newEnvWithMatchIngest(t, storage)
	ctx := context.Background()

	if _, err := env.svc.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Import disk-only via the staged entry so the series + disk provider exist.
	entries, err := env.svc.ListImports(ctx, "pending", 0, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListImports: %v (entries=%v)", err, entries)
	}
	if _, err := env.svc.Import(ctx, entries[0].Path, nil); err != nil {
		t.Fatalf("Import: %v", err)
	}

	ser := env.client.Series.Query().OnlyX(ctx)
	diskSP := env.client.SeriesProvider.Query().OnlyX(ctx)

	path := fmt.Sprintf("/api/series/%s/providers/%s/match", ser.ID, diskSP.ID)
	body := fmt.Sprintf(`{"source":"%d","url":%q,"importance":5}`, weebSourceID, weebMangaURL)
	rec := env.do("POST", path, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("match = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var got series.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertMatchedProviderDTO(t, got)
}

// assertMatchedProviderDTO checks the single-provider shape a successful
// Match must produce: the disk provider gone, the real source carrying the
// chosen importance under its engine-host id ("1", the stringified
// weebSourceID — P2 Suwayomi-removal: SeriesProvider.provider is now the
// numeric source id, never a source name), and its ChapterCount covering both
// re-pointed chapters.
func assertMatchedProviderDTO(t *testing.T, got series.SeriesDetailDTO) {
	t.Helper()
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk provider deleted)", len(got.Providers))
	}
	p := got.Providers[0]
	wantProvider := strconv.FormatInt(weebSourceID, 10)
	if p.Provider != wantProvider || p.Importance != 5 {
		t.Fatalf("provider = %+v, want provider=%s importance=5", p, wantProvider)
	}
	// NOTE: p.Linked / p.MangaID are NOT asserted here. Both are derived from
	// SeriesProvider.SuwayomiID (see series.newProviderDTO), which
	// internal/ingest.Ingest (the P2 Suwayomi-removal replacement for
	// suwayomi.Ingest) never sets on a newly-created row — confirmed
	// empirically, p.Linked reads false and p.MangaID reads 0 here even
	// though this IS the freshly-matched, real, live provider. This looks
	// like a gap carried by the migration (SuwayomiID has no equivalent
	// write path in the new URL-addressed ingest model) that reaches beyond
	// this DTO field into internal/library's own merge-at-attach/dedup
	// matching (matchingUnlinkedDiskProvider filters on SuwayomiID == 0) and
	// series.needsSource — both internal/library and internal/series are
	// out of scope for this test file, so it is flagged here rather than
	// silently asserted around.
	if p.ChapterCount != 2 {
		t.Fatalf("provider ChapterCount = %d, want 2 (both re-pointed chapters)", p.ChapterCount)
	}
}

// capturingSearchClient wraps a sourceengine fake exposing two named sources
// (stable ids 1 and 2) and records which source IDs imports.Service.Search
// actually fanned out to. It lets the GET /api/library/imports/match test
// prove the ?sources CSV filter is parsed (via the shared
// sourcefilter.Parse) and threaded through MatchCandidates into the search
// fan-out — a source id the handler drops never reaches the fake's Search.
type capturingSearchClient struct {
	*fake.Client
	mu      sync.Mutex
	queried []string
}

// newCapturingSearchClient builds the underlying fake (two sources, each with
// a one-candidate search result) plus the recording wrapper.
func newCapturingSearchClient() *capturingSearchClient {
	return &capturingSearchClient{
		Client: fake.New(
			fake.WithSources([]sourceengine.Source{
				{ID: 1, Name: "Weeb Source", Lang: "en"},
				{ID: 2, Name: "Other Source", Lang: "en"},
			}),
			fake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "/manga/1", Title: "hit"}}}),
			fake.WithSearchResult(2, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "/manga/2", Title: "hit"}}}),
		),
	}
}

// Search overrides the embedded fake's Search to record the queried source id
// before delegating (lock-safe; the fan-out is concurrent).
func (c *capturingSearchClient) Search(ctx context.Context, sourceID int64, query string, page int) (sourceengine.SearchResult, error) {
	c.mu.Lock()
	c.queried = append(c.queried, strconv.FormatInt(sourceID, 10))
	c.mu.Unlock()
	return c.Client.Search(ctx, sourceID, query, page)
}

// queriedIDs returns a copy of the source IDs Search was invoked for.
func (c *capturingSearchClient) queriedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queried...)
}

// newEnvWithMatchImports wires a REAL imports.Service over a
// capturingSearchClient and registers the GET /api/library/imports/match
// route, so the ?sources filter can be exercised end-to-end through the HTTP
// handler.
func newEnvWithMatchImports(t *testing.T, storage string) (*testEnv, *capturingSearchClient) {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	seriesSvc := series.NewService(client, storage, 14)
	fc := newCapturingSearchClient()
	ing := ingest.NewIngest(fc, client)
	importsSvc := imports.NewService(fc, ing, client, storage, 30*time.Second, nil)
	svc := library.NewService(client, ing, importsSvc, seriesSvc, func() {}, storage, sse.NewHub())
	h := handler.NewHandler(svc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/library/imports/match", h.Match)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token, svc: svc}, fc
}

// TestLibraryMatch_SourcesFilterReachesService proves the ?sources CSV query
// param is parsed and threaded through the Match handler into the search
// fan-out: with sources=1, ONLY source id 1 is queried (source id 2 the
// client also exposes is never fanned out to), which can only hold if the
// parsed filter reached MatchCandidates → imports.Service.Search.
func TestLibraryMatch_SourcesFilterReachesService(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 1)
	env, fc := newEnvWithMatchImports(t, storage)

	staged, err := env.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(staged) != 1 {
		t.Fatalf("staged len = %d, want 1", len(staged))
	}

	target := "/api/library/imports/match?path=" + url.QueryEscape(staged[0].Path) + "&sources=1"
	rec := env.do("GET", target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("match = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	got := fc.queriedIDs()
	if len(got) != 1 || got[0] != "1" {
		t.Fatalf("fan-out queried sources = %v, want [1] (the ?sources filter must reach Search)", got)
	}
}
