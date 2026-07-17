// Package library_test exercises the library-import HTTP handlers end-to-end
// through a real Echo instance (with RequireOwner wired) against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package library_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/library"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

const testSecret = "library-handler-test-secret"

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
	// svc is the same library.Service instance wired into e's routes. Tests
	// that need to stage ImportEntry rows use svc.Scan directly (the
	// synchronous path) rather than POST /api/library/scan, since that route
	// now launches the scan asynchronously (StartScan) and returns before any
	// row is guaranteed to exist.
	svc *library.Service
}

// newEnv wires a fully-authenticated Echo instance with the library routes
// behind RequireOwner (so the 401 proofs hit the real middleware) and a
// library.Service over a fresh testdb client + a throwaway temp storage
// root. ingest/imports/series are left nil — the tests exercised here
// (401s + scan-200) never reach code paths that dereference them.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	storage := t.TempDir()
	return newEnvWithStorage(t, storage)
}

// newEnvWithStorageSeeded is like newEnv but seeds the temp storage with one
// on-disk Kaizoku-style series before wiring the handler, so a scan finds
// something.
func newEnvWithStorageSeeded(t *testing.T) *testEnv {
	t.Helper()
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	return newEnvWithStorage(t, storage)
}

func newEnvWithStorage(t *testing.T, storage string) *testEnv {
	t.Helper()

	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	// seriesSvc is real (not nil) so a disk-only Import/ImportBatch's
	// GetSeries round-trip (§16) can actually resolve — ingest/importsSvc
	// stay nil since no test here attaches a Suwayomi match.
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())
	h := handler.NewHandler(svc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/library/scan", h.Scan)
	authed.GET("/library/imports", h.ListImports)
	authed.GET("/library/imports/match", h.Match)
	authed.POST("/library/import", h.Import)
	authed.POST("/library/import/batch", h.Batch)
	authed.POST("/library/imports/skip", h.Skip)
	authed.POST("/series/:id/providers", h.AddProvider)
	authed.POST("/series/:id/providers/batch", h.AddProviders)
	authed.POST("/series/:id/providers/dedup", h.DedupProviders)
	authed.POST("/library/dedup-providers", h.DedupAllProviders)
	authed.GET("/library/prefs", h.GetPrefs)
	authed.PUT("/library/prefs", h.PutPrefs)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token, svc: svc}
}

func (env *testEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

func (env *testEnv) doUnauth(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// writeKaizokuSeries writes N Kaizoku-style CBZs for one series under
// <storage>/<category>/<title>/. Each CBZ carries an embedded ComicInfo.xml
// and a Kaizoku filename bracket ([provider-scanlator][en] <title> <k>.cbz).
// Mirrors internal/library/scan_test.go's helper of the same name (a
// different package — no import cycle risk, small enough to duplicate here
// rather than export it from the service package for test-only reuse).
func writeKaizokuSeries(t *testing.T, storage, category, title, provider, scanlator string, n int) {
	t.Helper()
	dir := filepath.Join(storage, category, title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for k := 1; k <= n; k++ {
		number := fmt.Sprintf("%d", k)
		filename := fmt.Sprintf("[%s-%s][en] %s %d.cbz", provider, scanlator, title, k)

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		page, _ := zw.Create("001.jpg")
		_, _ = page.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9}) // minimal jpeg-ish bytes
		ciw, _ := zw.Create("ComicInfo.xml")
		_, _ = ciw.Write([]byte(`<?xml version="1.0"?><ComicInfo><Series>` + title +
			`</Series><Number>` + number + `</Number><Publisher>` + provider +
			`</Publisher><Translator>` + scanlator + `</Translator></ComicInfo>`))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filename), buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLibraryRoutes_RequireOwner proves every library route is behind
// RequireOwner: an unauthenticated request to any of them must 401.
func TestLibraryRoutes_RequireOwner(t *testing.T) {
	env := newEnv(t)
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/library/scan"},
		{"GET", "/api/library/imports"},
		{"POST", "/api/library/import"},
		{"POST", "/api/library/import/batch"},
		{"GET", "/api/library/imports/match?path=x"},
		{"POST", "/api/library/imports/skip"},
		{"POST", "/api/series/" + uuid.New().String() + "/providers"},
		{"POST", "/api/series/" + uuid.New().String() + "/providers/batch"},
		{"POST", "/api/series/" + uuid.New().String() + "/providers/dedup"},
		{"POST", "/api/library/dedup-providers"},
	} {
		rec := env.doUnauth(r.method, r.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauth = %d, want 401", r.method, r.path, rec.Code)
		}
	}
}

// TestLibraryScan_Accepted proves the happy path: POST /api/library/scan
// launches the async scan and returns 202 {started:true} immediately —
// it no longer blocks for the walk to finish (see library.Service.StartScan).
func TestLibraryScan_Accepted(t *testing.T) {
	env := newEnvWithStorageSeeded(t)
	rec := env.do("POST", "/api/library/scan", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scan = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Started bool `json:"started"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Started {
		t.Fatalf("started = %v, want true", got.Started)
	}
}

// TestLibraryDedupAll_Accepted proves POST /api/library/dedup-providers returns
// 202 {started:true} — the sweep runs detached.
func TestLibraryDedupAll_Accepted(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/dedup-providers", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	var got struct {
		Started bool `json:"started"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Started {
		t.Fatalf("started = %v, want true", got.Started)
	}
}

// TestLibraryScan_ConflictWhenInFlight proves the single-flight guard end to
// end through the HTTP handler: a second concurrent POST /api/library/scan
// while the first scan is still running gets 409 {started:false} rather than
// launching a second NFS walk.
func TestLibraryScan_ConflictWhenInFlight(t *testing.T) {
	env := newEnvWithStorageSeeded(t)

	first := env.do("POST", "/api/library/scan", "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first scan = %d, want 202 (%s)", first.Code, first.Body.String())
	}

	second := env.do("POST", "/api/library/scan", "")
	if second.Code != http.StatusConflict {
		t.Fatalf("second scan = %d, want 409 (%s)", second.Code, second.Body.String())
	}
	var got struct {
		Started bool `json:"started"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Started {
		t.Fatalf("started = %v, want false", got.Started)
	}
}

// TestLibraryImports_ListsStagedEntries proves the ListImports happy path
// end-to-end: a seeded on-disk series is staged (via the service's
// synchronous Scan — POST /library/scan is now async and returns before any
// row is guaranteed to exist), then GET /library/imports returns it with
// every field populated. This exercises the service's decode(row.Found)→
// distinctProviders reuse (not just a non-nil check) by asserting the
// recovered Providers list contains the source name.
func TestLibraryImports_ListsStagedEntries(t *testing.T) {
	env := newEnvWithStorageSeeded(t)

	if _, err := env.svc.Scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rec := env.do("GET", "/api/library/imports", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got []library.FoundSeriesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("list len = %d, want 1", len(got))
	}
	assertStagedEntry(t, got[0])
}

// assertStagedEntry checks the ListImports DTO for the single "My Series"
// fixture written by newEnvWithStorageSeeded. Providers is asserted by value
// (not just non-nil) so the service's decode(row.Found)→distinctProviders
// reuse is genuinely exercised. Extracted from the test body to keep each
// function's cyclomatic complexity within the cyclop ≤ 10 budget.
func assertStagedEntry(t *testing.T, f library.FoundSeriesDTO) {
	t.Helper()
	if f.Title != "My Series" || f.Category != "Manga" || f.ChapterCount != 2 {
		t.Fatalf("bad entry: %+v", f)
	}
	if len(f.Providers) != 1 || f.Providers[0] != "mangadex" {
		t.Fatalf("providers = %v, want [mangadex] (decode→distinctProviders reuse)", f.Providers)
	}
	if f.Status != "pending" || f.AlreadyInDB {
		t.Fatalf("status=%q alreadyInDb=%v, want pending/false", f.Status, f.AlreadyInDB)
	}
}

// seedImportEntry creates a minimal pending ImportEntry row with an explicit
// scanned_at so pagination ordering (ByScannedAt, ascending) is deterministic.
// Mirrors internal/library/list_test.go's helper of the same name (a
// different package — no import cycle risk, small enough to duplicate here
// rather than export it from the service package for test-only reuse).
func seedImportEntry(t *testing.T, client *ent.Client, ctx context.Context, path string, scannedAt time.Time) {
	t.Helper()
	if _, err := client.ImportEntry.Create().
		SetPath(path).SetTitle(path).SetCategory("Manga").
		SetChapterCount(1).SetStatus("pending").
		SetScannedAt(scannedAt).
		Save(ctx); err != nil {
		t.Fatalf("seed import entry %s: %v", path, err)
	}
}

// TestListImports_Paginated proves ?limit/?offset page the staged entries in
// scanned_at order end-to-end through the real HTTP handler.
func TestListImports_Paginated(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()

	base := time.Now()
	seedImportEntry(t, env.client, ctx, "/a", base)
	seedImportEntry(t, env.client, ctx, "/b", base.Add(time.Second))
	seedImportEntry(t, env.client, ctx, "/c", base.Add(2*time.Second))

	rec := env.do("GET", "/api/library/imports?limit=2&offset=0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("page1 = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var page1 []library.FoundSeriesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1) != 2 || page1[0].Path != "/a" || page1[1].Path != "/b" {
		t.Fatalf("page1 = %+v, want [/a /b]", page1)
	}

	rec2 := env.do("GET", "/api/library/imports?limit=2&offset=2", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("page2 = %d, want 200 (%s)", rec2.Code, rec2.Body.String())
	}
	var page2 []library.FoundSeriesDTO
	if err := json.Unmarshal(rec2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2) != 1 || page2[0].Path != "/c" {
		t.Fatalf("page2 = %+v, want [/c]", page2)
	}
}

// TestListImports_BadLimit proves a negative ?limit is rejected with 400.
func TestListImports_BadLimit(t *testing.T) {
	env := newEnv(t)
	rec := env.do("GET", "/api/library/imports?limit=-1", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryImports_BadStatusFilter proves ?status is validated against the
// closed enum.
func TestLibraryImports_BadStatusFilter(t *testing.T) {
	env := newEnv(t)
	rec := env.do("GET", "/api/library/imports?status=bogus", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryMatch_MissingPath proves ?path is required.
func TestLibraryMatch_MissingPath(t *testing.T) {
	env := newEnv(t)
	rec := env.do("GET", "/api/library/imports/match", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing path: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryImport_MissingPath proves the body requires a non-empty path.
func TestLibraryImport_MissingPath(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/import", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing path body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryImport_BadMatchEntry proves each matches[] entry is validated
// (Slice P: matches is now a LIST): a blank source in any entry is rejected
// with 400 before the service is ever called.
func TestLibraryImport_BadMatchEntry(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/import", `{"path":"/some/path","matches":[{"source":"","mangaId":7}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank match source: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryImport_EmptyMatchesIsValid proves an empty matches list is a
// valid request shape (import-only, no attach) — the path-not-staged 404
// surfaces, not a validation 400, so an empty list is not itself rejected.
func TestLibraryImport_EmptyMatchesIsValid(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/import", `{"path":"/nonexistent","matches":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty matches list: want 404 (entry not found), got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryBatch_PartialSuccess proves the batch endpoint end-to-end: two
// staged series import cleanly while a third, never-staged path fails —
// without aborting the other two (partial success, the whole point of the
// bulk endpoint for a 1000+ series migration).
func TestLibraryBatch_PartialSuccess(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "Series One", "mangadex", "Alpha", 1)
	writeKaizokuSeries(t, storage, "Manga", "Series Two", "mangadex", "Alpha", 1)
	env := newEnvWithStorage(t, storage)

	staged, err := env.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(staged) != 2 {
		t.Fatalf("staged len = %d, want 2", len(staged))
	}

	reqBody, err := json.Marshal(map[string][]string{
		"paths": {staged[0].Path, staged[1].Path, "/nonexistent/bogus"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := env.do("POST", "/api/library/import/batch", string(reqBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("batch = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got library.BatchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (%+v)", got.Imported, got)
	}
	if len(got.Failed) != 1 || got.Failed[0].Path != "/nonexistent/bogus" {
		t.Fatalf("failed = %+v, want one bogus entry", got.Failed)
	}
}

// TestLibraryBatch_EmptyPaths proves an empty paths list is rejected with 400
// rather than silently no-op-ing.
func TestLibraryBatch_EmptyPaths(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/import/batch", `{"paths":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty paths: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryBatch_TooManyPaths proves the maxBatchSize cap (500) is
// enforced — a single request can't demand unbounded synchronous work.
func TestLibraryBatch_TooManyPaths(t *testing.T) {
	env := newEnv(t)
	paths := make([]string, 501)
	for i := range paths {
		paths[i] = fmt.Sprintf("/p%d", i)
	}
	reqBody, err := json.Marshal(map[string][]string{"paths": paths})
	if err != nil {
		t.Fatal(err)
	}
	rec := env.do("POST", "/api/library/import/batch", string(reqBody))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too many paths: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryAddProvider_BadID proves :id is validated as a UUID.
func TestLibraryAddProvider_BadID(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/series/not-a-uuid/providers", `{"source":"mangadex","mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryAddProvider_InvalidBody proves the body is validated (missing
// source).
func TestLibraryAddProvider_InvalidBody(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/series/"+uuid.New().String()+"/providers", `{"mangaId":1,"importance":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestAddProvidersHandler exercises POST /api/series/:id/providers/batch
// (Slice P multi-attach) end-to-end through the real HTTP handler, mirroring
// the single-attach AddProvider handler's test patterns (validateID,
// mapServiceError, and — for the happy path — newEnvWithMatchIngest's real
// suwayomi.Ingest over fakeMatchClient, borrowed from the MatchDiskProvider
// tests in this same package). See library.Service.AddProviders. Each case
// is extracted to its own function (rather than inlined in the t.Run
// closures) to keep this dispatcher's cyclomatic complexity within budget.
func TestAddProvidersHandler(t *testing.T) {
	t.Run("401 without auth", testAddProvidersUnauthorized)
	t.Run("200 attaches a batch of two providers", testAddProvidersSuccess)
	t.Run("400 empty providers list", testAddProvidersEmptyList)
	t.Run("409 duplicate provider", testAddProvidersDuplicate)
	t.Run("404 unknown series id", testAddProvidersUnknownSeries)
}

func testAddProvidersUnauthorized(t *testing.T) {
	env := newEnv(t)
	path := "/api/series/" + uuid.New().String() + "/providers/batch"
	rec := env.doUnauth("POST", path, `{"providers":[{"source":"weeb","url":"/manga/1"}]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth = %d, want 401", rec.Code)
	}
}

func testAddProvidersSuccess(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	env := newEnvWithMatchIngest(t, storage)
	ctx := context.Background()

	if _, err := env.svc.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	entries, err := env.svc.ListImports(ctx, "pending", 0, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListImports: %v (entries=%v)", err, entries)
	}
	if _, err := env.svc.Import(ctx, entries[0].Path, nil); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ser := env.client.Series.Query().OnlyX(ctx)

	body := `{"providers":[{"source":"2","url":"/manga/91"},{"source":"3","url":"/manga/92"}]}`
	rec := env.do("POST", "/api/series/"+ser.ID.String()+"/providers/batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch attach = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got series.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Providers) != 3 {
		t.Fatalf("providers = %d, want 3 (disk + weebA + weebB)", len(got.Providers))
	}
}

func testAddProvidersEmptyList(t *testing.T) {
	env := newEnv(t)
	path := "/api/series/" + uuid.New().String() + "/providers/batch"
	rec := env.do("POST", path, `{"providers":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty providers: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func testAddProvidersDuplicate(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle("X").SetSlug("x").SaveX(ctx)
	env.client.SeriesProvider.Create().
		SetSeriesID(ser.ID).SetProvider("weebA").SetScanlator("").SetImportance(1).
		SaveX(ctx)

	body := `{"providers":[{"source":"weebA","url":"/manga/91"}]}`
	rec := env.do("POST", "/api/series/"+ser.ID.String()+"/providers/batch", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate provider: want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func testAddProvidersUnknownSeries(t *testing.T) {
	env := newEnv(t)
	path := "/api/series/" + uuid.New().String() + "/providers/batch"
	rec := env.do("POST", path, `{"providers":[{"source":"weebA","url":"/manga/91"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown series: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSkip_OK proves the happy path end-to-end: scanning a seeded temp
// storage directory stages a pending entry (via the service's synchronous
// Scan — POST /library/scan is now async), POST /library/imports/skip flips
// it to "skipped" (204), and a re-GET of /library/imports confirms the
// persisted status (§16 round-trip via re-fetch, not just the 204 code).
func TestSkip_OK(t *testing.T) {
	env := newEnvWithStorageSeeded(t)

	staged, err := env.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(staged) != 1 {
		t.Fatalf("staged len = %d, want 1", len(staged))
	}

	body := fmt.Sprintf(`{"path":%q}`, staged[0].Path)
	rec := env.do("POST", "/api/library/imports/skip", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("skip = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}

	listRec := env.do("GET", "/api/library/imports?status=skipped", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (%s)", listRec.Code, listRec.Body.String())
	}
	var got []library.FoundSeriesDTO
	if err := json.Unmarshal(listRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(got) != 1 || got[0].Path != staged[0].Path {
		t.Fatalf("skipped list = %+v, want [%s]", got, staged[0].Path)
	}
}

// TestSkip_NotFound proves an unstaged path 404s.
func TestSkip_NotFound(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/imports/skip", `{"path":"/nope"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("skip unknown path = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// TestSkip_MissingPath proves the body requires a non-empty path.
func TestSkip_MissingPath(t *testing.T) {
	env := newEnv(t)
	rec := env.do("POST", "/api/library/imports/skip", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing path body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}
