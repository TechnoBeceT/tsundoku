// Package metadata_test exercises the metadata-engine HTTP handlers end-to-end
// through a real Echo instance (with the RequireOwner middleware wired)
// against an ephemeral PostgreSQL instance (testdb) and a fake
// metadata.Provider (no real network access). Tests require Docker.
package metadata_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/metadata"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

const testSecret = "metadata-handler-test-secret"

// fakeProvider is a minimal, fully-configurable metadata.Provider double —
// mirrors internal/metadatasvc's own package-local test double (test doubles
// are not exported production code, so each black-box test package keeps its
// own small copy; same shape, different package).
type fakeProvider struct {
	key      string
	priority int

	searchResults []metadata.SearchResult
	matchResult   *metadata.SearchResult
	metas         map[string]metadata.SeriesMetadata
}

var _ metadata.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Key() string   { return f.key }
func (f *fakeProvider) ID() int       { return 0 }
func (f *fakeProvider) Priority() int { return f.priority }

func (f *fakeProvider) Search(context.Context, string, int) ([]metadata.SearchResult, error) {
	return f.searchResults, nil
}

func (f *fakeProvider) GetSeriesMetadata(_ context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	m, ok := f.metas[remoteID]
	if !ok {
		return metadata.SeriesMetadata{}, errors.New("fakeProvider: no metadata for remote id " + remoteID)
	}
	return m, nil
}

func (f *fakeProvider) GetSeriesCover(context.Context, string) ([]byte, string, error) {
	return nil, "", errors.New("fakeProvider: GetSeriesCover not implemented")
}

func (f *fakeProvider) Match(context.Context, metadata.MatchQuery) (*metadata.SearchResult, error) {
	return f.matchResult, nil
}

// testEnv bundles the wired Echo app, the DB client, a valid owner token, and
// the storage root.
type testEnv struct {
	e       *echo.Echo
	client  *ent.Client
	token   string
	storage string
}

// newTestEnv stands up a fully-wired Echo: the metadata routes registered
// behind RequireOwner (so the 401 proofs exercise the real middleware), a
// metadatasvc.Service over a fresh testdb client + a single fakeProvider
// ("anilist"), the shared series.Service (so Identify/SetCover can return the
// refreshed SeriesDetailDTO), and a valid owner Bearer token.
func newTestEnv(t *testing.T, fp *fakeProvider) *testEnv {
	t.Helper()
	client := testdb.New(t)
	storage := t.TempDir()
	registry := metadata.NewRegistry(fp)
	// WithHTTPClient is the test seam (metadatasvc.NewService's doc comment):
	// the PRODUCTION default client refuses to dial loopback addresses, which
	// would block TestSetCover_Success's local httptest.Server. Every other
	// handler test in this file never reaches the network, so the plain
	// client is harmless for them.
	metaSvc := metadatasvc.NewService(client, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))
	return wireTestEnv(t, client, storage, metaSvc)
}

// wireTestEnv is the shared tail every constructor in this file uses (see
// newTestEnv and metadatasvcNewServiceWithSourceCover's callers): it builds
// the Echo app (routes behind RequireOwner) + a valid owner Bearer token
// around an already-constructed metaSvc. Split out because the "source"-kind
// SetCover tests must seed a SeriesProvider row (to know its generated UUID)
// BEFORE the SourceCoverFetcher — and therefore metaSvc — can be built,
// which newTestEnv's all-in-one shape doesn't allow for.
func wireTestEnv(t *testing.T, client *ent.Client, storage string, metaSvc *metadatasvc.Service) *testEnv {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	seriesSvc := seriessvc.NewService(client, storage, 14)
	h := handler.NewHandler(metaSvc, seriesSvc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/metadata/search", h.Search)
	authed.POST("/series/:id/metadata/identify", h.Identify)
	authed.GET("/series/:id/metadata/covers", h.Covers)
	authed.POST("/series/:id/cover", h.SetCover)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token, storage: storage}
}

func (env *testEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// seedSeries creates a minimal categorized series ("Manga"/title) with no
// providers.
func seedSeries(ctx context.Context, t *testing.T, db *ent.Client, title, slug string) uuid.UUID {
	t.Helper()
	catID, err := category.IDByName(ctx, db, "Manga")
	if err != nil {
		t.Fatalf("category.IDByName: %v", err)
	}
	s := db.Series.Create().SetTitle(title).SetSlug(slug).SetCategoryID(catID).SaveX(ctx)
	return s.ID
}

// withSeriesDir creates the on-disk library folder for a "Manga"/title series
// under storage — required before disk.WriteMetadata/SaveCover will persist
// anything (neither ever creates the series directory itself).
func withSeriesDir(t *testing.T, storage, title string) {
	t.Helper()
	if err := os.MkdirAll(disk.SeriesDir(storage, "Manga", title), 0o750); err != nil {
		t.Fatalf("mkdir series dir: %v", err)
	}
}

// seedSeriesProviderWithCover creates a SeriesProvider row for seriesID with
// a cover_url — the fixture the source-candidate/source-SetCover tests need.
// Package-local copy of internal/metadatasvc's own test helper (black-box
// test packages don't share unexported fixtures).
func seedSeriesProviderWithCover(ctx context.Context, t *testing.T, db *ent.Client, seriesID uuid.UUID, provider, providerName, coverURL string) uuid.UUID {
	t.Helper()
	p := db.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(provider).
		SetProviderName(providerName).
		SetCoverURL(coverURL).
		SetImportance(1).
		SaveX(ctx)
	return p.ID
}

// fakeSourceCoverFetcher is a minimal metadatasvc.SourceCoverFetcher double —
// mirrors internal/metadatasvc's own package-local copy (test doubles are not
// exported production code).
type fakeSourceCoverFetcher struct {
	seriesID   uuid.UUID
	providerID uuid.UUID
	data       []byte
	ext        string
}

func (f *fakeSourceCoverFetcher) SourceCoverBytes(_ context.Context, seriesID, providerID uuid.UUID) ([]byte, string, error) {
	if seriesID != f.seriesID || providerID != f.providerID {
		return nil, "", errors.New("fakeSourceCoverFetcher: unexpected series/provider id")
	}
	return f.data, f.ext, nil
}

// TestAuthz_AllRoutesReject401 asserts every metadata route is behind
// RequireOwner: an unauthenticated request is rejected before it ever reaches
// the handler (so a real DB call is never even attempted).
func TestAuthz_AllRoutesReject401(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})
	id := uuid.New().String()

	routes := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/metadata/search?q=foo"},
		{http.MethodPost, "/api/series/" + id + "/metadata/identify"},
		{http.MethodGet, "/api/series/" + id + "/metadata/covers"},
		{http.MethodPost, "/api/series/" + id + "/cover"},
	}
	for _, rt := range routes {
		r := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", rt.method, rt.path, rec.Code, http.StatusUnauthorized)
		}
	}
}

// TestSearch_Success asserts the candidate gallery round-trips through the
// SearchResultDTO mapper.
func TestSearch_Success(t *testing.T) {
	fp := &fakeProvider{
		key: "anilist",
		searchResults: []metadata.SearchResult{
			{Provider: "anilist", RemoteID: "42", Title: "Chainsaw Man", URL: "https://anilist.co/manga/42", CoverURL: "https://img.test/42.jpg", Year: 2018},
		},
	}
	env := newTestEnv(t, fp)

	rec := env.do(http.MethodGet, "/api/metadata/search?q=Chainsaw", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out []handler.SearchResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	got := out[0]
	if got.Provider != "anilist" || got.RemoteID != "42" || got.Title != "Chainsaw Man" || got.Year != 2018 {
		t.Errorf("SearchResultDTO = %+v, want {anilist 42 Chainsaw Man ... 2018}", got)
	}
}

// TestSearch_MissingQuery asserts an empty/absent ?q is a 400.
func TestSearch_MissingQuery(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})

	rec := env.do(http.MethodGet, "/api/metadata/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestIdentify_Success asserts a valid identify call persists the merged
// metadata and returns the refreshed SeriesDetailDTO reflecting it.
func TestIdentify_Success(t *testing.T) {
	ctx := context.Background()
	fp := &fakeProvider{
		key: "anilist",
		metas: map[string]metadata.SeriesMetadata{
			"42": {
				Title:       "Chainsaw Man",
				Description: "Denji becomes Chainsaw Man.",
				Genres:      []string{"Action"},
				Year:        2018,
			},
		},
	}
	env := newTestEnv(t, fp)
	id := seedSeries(ctx, t, env.client, "Chainsaw Man", "chainsaw-man")

	body := `{"provider":"anilist","remoteId":"42"}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/metadata/identify", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Genres) != 1 || out.Genres[0] != "Action" {
		t.Errorf("Genres = %v, want [Action]", out.Genres)
	}
	if out.Year != 2018 {
		t.Errorf("Year = %d, want 2018", out.Year)
	}
	if out.MetadataSource == nil || out.MetadataSource.Ref != "anilist" || out.MetadataSource.RemoteID != "42" {
		t.Errorf("MetadataSource = %+v, want {metadata anilist 42 ...}", out.MetadataSource)
	}
}

// TestIdentify_SeriesNotFound asserts an unknown series id is a 404.
func TestIdentify_SeriesNotFound(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist", metas: map[string]metadata.SeriesMetadata{"42": {Title: "X"}}})

	body := `{"provider":"anilist","remoteId":"42"}`
	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/metadata/identify", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestIdentify_UnknownProvider asserts a provider key the registry does not
// hold is a 400 (a bad request, not a missing resource).
func TestIdentify_UnknownProvider(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, &fakeProvider{key: "anilist"})
	id := seedSeries(ctx, t, env.client, "Some Series", "some-series")

	body := `{"provider":"does-not-exist","remoteId":"1"}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/metadata/identify", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestIdentify_MissingFields asserts an empty provider/remoteId is a 400
// before the service is ever called.
func TestIdentify_MissingFields(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})

	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/metadata/identify", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCovers_Success asserts the aggregated cover gallery round-trips through
// the CoverCandidateDTO mapper.
func TestCovers_Success(t *testing.T) {
	ctx := context.Background()
	fp := &fakeProvider{
		key: "anilist",
		searchResults: []metadata.SearchResult{
			{Provider: "anilist", RemoteID: "42", Title: "Chainsaw Man", CoverURL: "https://img.test/42.jpg"},
		},
	}
	env := newTestEnv(t, fp)
	id := seedSeries(ctx, t, env.client, "Chainsaw Man", "chainsaw-man")

	rec := env.do(http.MethodGet, "/api/series/"+id.String()+"/metadata/covers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out []handler.CoverCandidateDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	got := out[0]
	if got.SourceKind != "metadata" || got.SourceRef != "anilist" || got.CoverURL != "https://img.test/42.jpg" {
		t.Errorf("CoverCandidateDTO = %+v, want {metadata anilist https://img.test/42.jpg ...}", got)
	}
}

// TestCovers_SeriesNotFound asserts an unknown series id is a 404.
func TestCovers_SeriesNotFound(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})

	rec := env.do(http.MethodGet, "/api/series/"+uuid.New().String()+"/metadata/covers", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestSetCover_Success asserts the owner's cover pick fetches, caches, and
// records cover_source, returning the refreshed SeriesDetailDTO.
func TestSetCover_Success(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, &fakeProvider{key: "anilist"})
	id := seedSeries(ctx, t, env.client, "Cover Series", "cover-series")
	withSeriesDir(t, env.storage, "Cover Series")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-png-bytes"))
	}))
	t.Cleanup(srv.Close)

	body := `{"sourceKind":"metadata","sourceRef":"anilist","coverUrl":"` + srv.URL + `/cover.png"}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/cover", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.CoverSource == nil || out.CoverSource.Ref != "anilist" {
		t.Errorf("CoverSource = %+v, want {metadata anilist ...}", out.CoverSource)
	}
	// NOTE: out.CoverURL is deliberately NOT asserted non-empty here.
	// SeriesDisplay/CoverBytes (internal/series) still resolve the cover proxy
	// path ONLY from a SeriesProvider's cover_url (the M10 model) — the
	// metadata-engine's independent cover_source (set above) is not yet a
	// fallback source for a providerless series. Wiring that fallback is a
	// separate, not-yet-scheduled task (plan/metadata-engine-phase1 C3's
	// "SeriesDisplay M10 fallback"); this C2 slice deliberately leaves that
	// resolution untouched. The cache write itself is verified at the DB level
	// below instead.
	row := env.client.Series.GetX(ctx, id)
	if row.CoverFile == "" || row.CoverSourceURL == "" || row.CoverVersion == "" {
		t.Errorf("Series row cover columns not populated: file=%q sourceUrl=%q version=%q",
			row.CoverFile, row.CoverSourceURL, row.CoverVersion)
	}
}

// TestSetCover_InvalidBody asserts a non-absolute-http(s) coverUrl (and blank
// sourceKind/sourceRef) is a 400 before the service ever fetches anything.
func TestSetCover_InvalidBody(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})
	id := uuid.New().String()

	cases := []string{
		`{"sourceKind":"","sourceRef":"anilist","coverUrl":"https://img.test/x.jpg"}`,
		`{"sourceKind":"bogus","sourceRef":"anilist","coverUrl":"https://img.test/x.jpg"}`,
		`{"sourceKind":"metadata","sourceRef":"","coverUrl":"https://img.test/x.jpg"}`,
		`{"sourceKind":"metadata","sourceRef":"anilist","coverUrl":"not-a-url"}`,
		`{"sourceKind":"metadata","sourceRef":"anilist","coverUrl":""}`,
	}
	for _, body := range cases {
		rec := env.do(http.MethodPost, "/api/series/"+id+"/cover", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestSetCover_SeriesNotFound asserts an unknown series id is a 404 (after
// passing validation, since the cover URL must be well-formed to reach the
// service at all).
func TestSetCover_SeriesNotFound(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})

	body := `{"sourceKind":"metadata","sourceRef":"anilist","coverUrl":"https://img.test/x.jpg"}`
	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/cover", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCovers_IncludesSourceCandidate is Feature 2's end-to-end proof at the
// HTTP layer: the gallery includes the series' own library source alongside
// the metadata-provider hit, with the proxy CoverURL (never the raw
// Suwayomi-relative cover_url).
func TestCovers_IncludesSourceCandidate(t *testing.T) {
	ctx := context.Background()
	fp := &fakeProvider{
		key: "anilist",
		searchResults: []metadata.SearchResult{
			{Provider: "anilist", RemoteID: "42", Title: "Chainsaw Man", CoverURL: "https://img.test/42.jpg"},
		},
	}
	env := newTestEnv(t, fp)
	id := seedSeries(ctx, t, env.client, "Chainsaw Man", "chainsaw-man")
	providerID := seedSeriesProviderWithCover(ctx, t, env.client, id, "7", "Comix", "/api/v1/manga/7/thumbnail")

	rec := env.do(http.MethodGet, "/api/series/"+id.String()+"/metadata/covers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out []handler.CoverCandidateDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (one metadata hit + one source): %+v", len(out), out)
	}

	var sourceCand *handler.CoverCandidateDTO
	for i := range out {
		if out[i].SourceKind == "source" {
			sourceCand = &out[i]
		}
	}
	if sourceCand == nil {
		t.Fatalf("no source-kind candidate in %+v", out)
	}
	wantURL := "/api/series/" + id.String() + "/providers/" + providerID.String() + "/cover"
	if sourceCand.SourceRef != providerID.String() || sourceCand.CoverURL != wantURL || sourceCand.Label != "Comix" {
		t.Errorf("source candidate = %+v, want {source %s %s Comix}", sourceCand, providerID, wantURL)
	}
}

// TestSetCover_Source is Feature 2's SetCover round-trip at the HTTP layer: a
// "source"-kind pick (whose coverUrl is the same-origin proxy path, not an
// absolute URL) is accepted by validation, resolved through the attached
// SourceCoverFetcher, and the refreshed SeriesDetailDTO reflects the new
// cover_source.
func TestSetCover_Source(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	id := seedSeries(ctx, t, db, "Source Pick Series", "source-pick-series")
	withSeriesDir(t, storage, "Source Pick Series")
	providerID := seedSeriesProviderWithCover(ctx, t, db, id, "7", "Comix", "/api/v1/manga/7/thumbnail")

	scf := &fakeSourceCoverFetcher{seriesID: id, providerID: providerID, data: []byte("src-bytes"), ext: "png"}
	env := wireTestEnv(t, db, storage, metadatasvcNewServiceWithSourceCover(t, db, storage, scf))

	coverURL := "/api/series/" + id.String() + "/providers/" + providerID.String() + "/cover"
	body := `{"sourceKind":"source","sourceRef":"` + providerID.String() + `","coverUrl":"` + coverURL + `"}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/cover", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.CoverSource == nil || out.CoverSource.Kind != "source" || out.CoverSource.Ref != providerID.String() {
		t.Errorf("CoverSource = %+v, want {source %s ...}", out.CoverSource, providerID)
	}
}

// TestSetCover_SourceCoverUrlNeedNotBeAbsolute confirms the validator no
// longer rejects a "source"-kind body whose coverUrl is a same-origin path
// (this was the gap that made Feature 2's proxy CoverURL unusable end-to-end:
// a candidate's own coverUrl would always have failed the old
// metadata-only absolute-http(s) rule).
func TestSetCover_SourceCoverUrlNeedNotBeAbsolute(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	id := seedSeries(ctx, t, db, "Relative Cover Series", "relative-cover-series")
	withSeriesDir(t, storage, "Relative Cover Series")
	providerID := seedSeriesProviderWithCover(ctx, t, db, id, "7", "Comix", "/api/v1/manga/7/thumbnail")

	scf := &fakeSourceCoverFetcher{seriesID: id, providerID: providerID, data: []byte("bytes"), ext: "jpg"}
	env := wireTestEnv(t, db, storage, metadatasvcNewServiceWithSourceCover(t, db, storage, scf))

	body := `{"sourceKind":"source","sourceRef":"` + providerID.String() + `","coverUrl":"/api/series/` + id.String() + `/providers/` + providerID.String() + `/cover"}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/cover", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (relative proxy path must pass validation for sourceKind=source); body: %s", rec.Code, rec.Body.String())
	}
}

// TestSetCover_SourceBlankCoverUrlRejected confirms an empty coverUrl is
// still a 400 for sourceKind=="source" (only the absolute-http(s) shape
// requirement is relaxed, not the "must be present" requirement).
func TestSetCover_SourceBlankCoverUrlRejected(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{key: "anilist"})
	id := uuid.New().String()

	body := `{"sourceKind":"source","sourceRef":"` + uuid.New().String() + `","coverUrl":""}`
	rec := env.do(http.MethodPost, "/api/series/"+id+"/cover", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// metadatasvcNewServiceWithSourceCover builds a metadatasvc.Service over an
// already-seeded db/storage with scf attached — split out so both HTTP-level
// source-cover tests share it without duplicating the WithHTTPClient +
// WithSourceCoverFetcher construction.
func metadatasvcNewServiceWithSourceCover(t *testing.T, db *ent.Client, storage string, scf metadatasvc.SourceCoverFetcher) *metadatasvc.Service {
	t.Helper()
	registry := metadata.NewRegistry()
	return metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{})).
		WithSourceCoverFetcher(scf)
}
