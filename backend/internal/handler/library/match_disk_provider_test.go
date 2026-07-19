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
// exercise the full handler → service → ingest chain. The SSE hub is stored on
// the env so a test can subscribe and wait for the provider.merged completion
// event the now-async match broadcasts.
func newEnvWithMatchIngest(t *testing.T, storage string) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	seriesSvc := series.NewService(client, storage, 14)
	ing := ingest.NewIngest(newMatchClient(), client)
	hub := sse.NewHub()
	svc := library.NewService(client, ing, nil, seriesSvc, func() {}, storage, hub)
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
	return &testEnv{e: e, client: client, token: token, svc: svc, hub: hub}
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

// TestMatchDiskProvider_UnknownProviderSurfacesOnSSE proves that a "deeper"
// failure — an unknown providerId (not belonging to the series) — now surfaces on
// the provider.merged SSE event's error field, NOT as a synchronous 4xx. With the
// async hardening (GAP-096) the handler validates only the request SHAPE
// synchronously (bad id/body → 400, tested above) and returns 202 for a
// well-formed request; the ErrProviderNotInSeries lookup happens inside the
// detached merge, so its failure rides the completion event the FE listens on.
func TestMatchDiskProvider_UnknownProviderSurfacesOnSSE(t *testing.T) {
	storage := t.TempDir()
	env := newEnvWithMatchIngest(t, storage)
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle("X").SetSlug("x").SaveX(ctx)

	events, unsubscribe := env.hub.Subscribe()
	defer unsubscribe()

	path := fmt.Sprintf("/api/series/%s/providers/%s/match", ser.ID, uuid.New())
	rec := env.do("POST", path, `{"source":"weeb","url":"/manga/1","importance":1}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unknown provider: want 202 (well-formed request, launched async), got %d (%s)", rec.Code, rec.Body.String())
	}
	assertStarted(t, rec.Body.Bytes(), true)

	// The background merge fails (provider not in series) and reports it on the
	// completion event's error field — a CLEAN, caller-safe sentinel message (the
	// SSE side-channel genericises exactly like the central error middleware).
	// Waiting also ensures the goroutine finishes before the testdb is torn down.
	me := awaitMergeEvent(t, events, ser.ID.String())
	if me.Error != "provider does not belong to series" {
		t.Fatalf("provider.merged Error = %q, want the clean sentinel message", me.Error)
	}
}

// awaitMergeEvent blocks until a provider.merged SSE event for wantSeriesID
// arrives (returning it) or a timeout fires — the error-tolerant sibling of
// waitForMerge used by tests that expect the merge to FAIL.
func awaitMergeEvent(t *testing.T, events <-chan sse.Event, wantSeriesID string) library.MergeEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != "provider.merged" {
				continue
			}
			raw, err := json.Marshal(ev.Data)
			if err != nil {
				t.Fatalf("marshal merge event: %v", err)
			}
			var me library.MergeEvent
			if err := json.Unmarshal(raw, &me); err != nil {
				t.Fatalf("decode merge event: %v", err)
			}
			if me.SeriesID == wantSeriesID {
				return me
			}
		case <-deadline:
			t.Fatal("timed out waiting for provider.merged SSE event")
		}
	}
}

// TestMatchDiskProvider_HappyPath exercises the full async round-trip through
// the real HTTP handler: a disk-imported series' unlinked provider is matched to
// a real engine-host source. The endpoint returns 202 {started:true}
// immediately, the merge runs detached in the background, a provider.merged SSE
// event fires on completion, and the subsequently-fetched SeriesDetail shows the
// disk provider gone and the new one carrying the chosen importance and both
// re-pointed chapters.
func TestMatchDiskProvider_HappyPath(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	env := newEnvWithMatchIngest(t, storage)
	ctx := context.Background()

	if _, err := env.svc.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Import disk-only via the staged entry so the series + disk provider exist.
	entries, err := env.svc.ListImports(ctx, "pending", "", 0, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListImports: %v (entries=%v)", err, entries)
	}
	if _, err := env.svc.Import(ctx, entries[0].Path, nil); err != nil {
		t.Fatalf("Import: %v", err)
	}

	ser := env.client.Series.Query().OnlyX(ctx)
	diskSP := env.client.SeriesProvider.Query().OnlyX(ctx)

	// Subscribe BEFORE the request so the provider.merged completion event can't
	// be missed (it fires when the detached merge finishes).
	events, unsubscribe := env.hub.Subscribe()
	defer unsubscribe()

	path := fmt.Sprintf("/api/series/%s/providers/%s/match", ser.ID, diskSP.ID)
	body := fmt.Sprintf(`{"source":"%d","url":%q,"importance":5}`, weebSourceID, weebMangaURL)
	rec := env.do("POST", path, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("match = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	assertStarted(t, rec.Body.Bytes(), true)

	// Wait for the async merge to complete via its SSE completion event.
	waitForMerge(t, events, ser.ID.String())

	// Now the authoritative detail reflects the completed merge (§16 — the FE
	// refetches on the event; here we fetch via the service the same way).
	got, err := env.svc.SeriesDetail(ctx, ser.ID)
	if err != nil {
		t.Fatalf("SeriesDetail after merge: %v", err)
	}
	assertMatchedProviderDTO(t, got)
}

// assertStarted decodes a {started:bool} match/scan response body and asserts it.
func assertStarted(t *testing.T, body []byte, want bool) {
	t.Helper()
	var resp struct {
		Started bool `json:"started"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode started response: %v (%s)", err, body)
	}
	if resp.Started != want {
		t.Fatalf("started = %v, want %v", resp.Started, want)
	}
}

// waitForMerge blocks until a SUCCESSFUL provider.merged SSE event for
// wantSeriesID arrives (failing if one reports an error) or a timeout fires. It
// mirrors how the FE waits on the event before refetching.
func waitForMerge(t *testing.T, events <-chan sse.Event, wantSeriesID string) {
	t.Helper()
	if me := awaitMergeEvent(t, events, wantSeriesID); me.Error != "" {
		t.Fatalf("provider.merged reported error: %s", me.Error)
	}
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
	// p.Linked must be true: Match's whole point is attributing the disk
	// chapters to a REAL, live source, and Provider ("1", the stringified
	// weebSourceID) parses as numeric — series.IsLinkedProvider — so the DTO
	// now correctly reports it as linked (P2 slice 3c fixed the SuwayomiID==0
	// regression this comment used to document: internal/ingest never sets
	// SuwayomiID on a newly-created row, so the old suwayomi_id!=0 check
	// always read false for a freshly-matched provider).
	if !p.Linked {
		t.Fatalf("provider Linked = false, want true (Provider=%q is a numeric source id)", p.Provider)
	}
	// p.MangaID is always 0 now — see ProviderDTO's doc comment (P2
	// Suwayomi-removal: no url-addressed manga-id equivalent).
	if p.MangaID != 0 {
		t.Fatalf("provider MangaID = %d, want 0", p.MangaID)
	}
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
