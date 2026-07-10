package library_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/library"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"

	"github.com/labstack/echo/v4"
)

// fakeMatchClient is a minimal suwayomi.Client for the MatchDiskProvider
// handler round-trip test: FetchChapters reports the same two chapters
// ("1"/"2") the disk fixture carries, and MangaMeta returns a valid Manga so
// suwayomi.Ingest.upsertSeriesProvider does not fail. Duplicated from
// internal/library/provider_test.go's fakeAddProviderClient — a different Go
// package (this is internal/handler/library), no import-cycle risk, and
// small enough that exporting a shared test-only type isn't worth the extra
// production-adjacent surface.
type fakeMatchClient struct{}

func (f *fakeMatchClient) Sources(ctx context.Context) ([]suwayomi.Source, error) { return nil, nil }
func (f *fakeMatchClient) Search(ctx context.Context, sourceID, query string) ([]suwayomi.Manga, error) {
	return nil, nil
}
func (f *fakeMatchClient) Browse(ctx context.Context, sourceID string, t suwayomi.BrowseType, page int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (f *fakeMatchClient) FetchChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	one, two := 1.0, 2.0
	return []suwayomi.Chapter{
		{ID: 101, Index: 0, Name: "Chapter 1", Number: &one},
		{ID: 102, Index: 1, Name: "Chapter 2", Number: &two},
	}, nil
}
func (f *fakeMatchClient) MangaChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeMatchClient) ChapterPages(ctx context.Context, chapterID int) ([]string, error) {
	return nil, nil
}
func (f *fakeMatchClient) MangaMeta(ctx context.Context, mangaID int) (suwayomi.Manga, error) {
	return suwayomi.Manga{ID: mangaID, Title: "My Series"}, nil
}
func (f *fakeMatchClient) FetchMangaDetails(ctx context.Context, mangaID int) (suwayomi.Manga, error) {
	return suwayomi.Manga{ID: mangaID, Title: "My Series"}, nil
}
func (f *fakeMatchClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	return nil, "", errors.New("PageBytes: not configured")
}
func (f *fakeMatchClient) ServerSettings(ctx context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeMatchClient) SetServerSettings(ctx context.Context, patch suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeMatchClient) Extensions(ctx context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeMatchClient) SetExtensionState(ctx context.Context, pkgName string, action suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeMatchClient) FetchExtensions(ctx context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeMatchClient) ExtensionRepos(ctx context.Context) ([]string, error) { return nil, nil }
func (f *fakeMatchClient) SetExtensionRepos(ctx context.Context, repos []string) error {
	return nil
}
func (f *fakeMatchClient) SourcePreferences(ctx context.Context, sourceID string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeMatchClient) SetSourcePreference(ctx context.Context, sourceID string, position int, value suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeMatchClient) ExtensionSources(ctx context.Context, pkgName string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (f *fakeMatchClient) SetSourceEnabled(ctx context.Context, sourceID string, enabled bool) error {
	return nil
}

// newEnvWithMatchIngest is like newEnvWithStorage but wires a REAL
// suwayomi.Ingest over fakeMatchClient (instead of nil) and registers the
// MatchDiskProvider route, so the happy-path round-trip test below can
// exercise the full handler → service → ingest chain.
func newEnvWithMatchIngest(t *testing.T, storage string) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	seriesSvc := series.NewService(client, storage, 14)
	ingest := suwayomi.NewIngest(&fakeMatchClient{}, client)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())
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
	rec := env.doUnauth("POST", path, `{"source":"weeb","mangaId":1,"importance":1}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth = %d, want 401", rec.Code)
	}
}

// TestMatchDiskProvider_BadSeriesID proves :id is validated as a UUID.
func TestMatchDiskProvider_BadSeriesID(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/not-a-uuid/providers/%s/match", uuid.New())
	rec := env.do("POST", path, `{"source":"weeb","mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad series id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_BadProviderID proves :providerId is validated as a UUID.
func TestMatchDiskProvider_BadProviderID(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/%s/providers/not-a-uuid/match", uuid.New())
	rec := env.do("POST", path, `{"source":"weeb","mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad provider id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_InvalidBody proves the body is validated (missing source).
func TestMatchDiskProvider_InvalidBody(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	path := fmt.Sprintf("/api/series/%s/providers/%s/match", uuid.New(), uuid.New())
	rec := env.do("POST", path, `{"mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_UnknownProvider404s proves an unknown providerId (not
// belonging to the series) maps to 400 via ErrProviderNotInSeries.
func TestMatchDiskProvider_UnknownProvider400s(t *testing.T) {
	storage := t.TempDir()
	env := newEnvWithMatchIngest(t, storage)
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle("X").SetSlug("x").SaveX(ctx)

	path := fmt.Sprintf("/api/series/%s/providers/%s/match", ser.ID, uuid.New())
	rec := env.do("POST", path, `{"source":"weeb","mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMatchDiskProvider_HappyPath exercises the full round-trip through the
// real HTTP handler: a disk-imported series' unlinked provider is matched to
// a real Suwayomi source, and the response carries the refreshed
// SeriesDetailDTO with the disk provider gone and the new one Linked+ranked.
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
	rec := env.do("POST", path, `{"source":"weeb","mangaId":99,"importance":5}`)
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
// Match must produce: the disk provider gone, the real source Linked and
// carrying the chosen importance, and its ChapterCount covering both
// re-pointed chapters.
func assertMatchedProviderDTO(t *testing.T, got series.SeriesDetailDTO) {
	t.Helper()
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk provider deleted)", len(got.Providers))
	}
	p := got.Providers[0]
	if !p.Linked || p.Provider != "weeb" || p.Importance != 5 {
		t.Fatalf("provider = %+v, want linked=true provider=weeb importance=5", p)
	}
	if p.ChapterCount != 2 {
		t.Fatalf("provider ChapterCount = %d, want 2 (both re-pointed chapters)", p.ChapterCount)
	}
}

// capturingSearchClient embeds fakeMatchClient but exposes two named sources
// and records which source IDs imports.Service.Search actually fanned out to.
// It lets the GET /api/library/imports/match test prove the ?sources CSV
// filter is parsed (via the shared sourcefilter.Parse) and threaded through
// MatchCandidates into the search fan-out — a source id the handler drops
// never reaches the fake's Search.
type capturingSearchClient struct {
	*fakeMatchClient
	mu      sync.Mutex
	queried []string
}

func (c *capturingSearchClient) Sources(ctx context.Context) ([]suwayomi.Source, error) {
	return []suwayomi.Source{
		{ID: "weeb", Name: "Weeb Source", Lang: "en"},
		{ID: "other", Name: "Other Source", Lang: "en"},
	}, nil
}

func (c *capturingSearchClient) Search(ctx context.Context, sourceID, query string) ([]suwayomi.Manga, error) {
	c.mu.Lock()
	c.queried = append(c.queried, sourceID)
	c.mu.Unlock()
	return []suwayomi.Manga{{ID: 1, Title: query}}, nil
}

// queriedIDs returns a copy of the source IDs Search was invoked for (lock-safe;
// the fan-out is concurrent).
func (c *capturingSearchClient) queriedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queried...)
}

// newEnvWithMatchImports wires a REAL imports.Service over a capturingSearchClient
// and registers the GET /api/library/imports/match route, so the ?sources filter
// can be exercised end-to-end through the HTTP handler.
func newEnvWithMatchImports(t *testing.T, storage string) (*testEnv, *capturingSearchClient) {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	seriesSvc := series.NewService(client, storage, 14)
	fc := &capturingSearchClient{fakeMatchClient: &fakeMatchClient{}}
	ingest := suwayomi.NewIngest(fc, client)
	importsSvc := imports.NewService(fc, ingest, client, storage, 30*time.Second, nil)
	svc := library.NewService(client, ingest, importsSvc, seriesSvc, func() {}, storage, sse.NewHub())
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
// fan-out: with sources=weeb, ONLY the "weeb" source is queried (the "other"
// source the client also exposes is never fanned out to), which can only hold
// if the parsed filter reached MatchCandidates → imports.Service.Search.
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

	target := "/api/library/imports/match?path=" + url.QueryEscape(staged[0].Path) + "&sources=weeb"
	rec := env.do("GET", target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("match = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	got := fc.queriedIDs()
	if len(got) != 1 || got[0] != "weeb" {
		t.Fatalf("fan-out queried sources = %v, want [weeb] (the ?sources filter must reach Search)", got)
	}
}
