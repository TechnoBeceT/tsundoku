// Package library_test exercises the library-import HTTP handlers end-to-end
// through a real Echo instance (with RequireOwner wired) against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package library_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/library"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

const testSecret = "library-handler-test-secret"

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
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
	svc := library.NewService(client, nil, nil, nil, func() {}, storage)
	h := handler.NewHandler(svc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/library/scan", h.Scan)
	authed.GET("/library/imports", h.ListImports)
	authed.GET("/library/imports/match", h.Match)
	authed.POST("/library/import", h.Import)
	authed.POST("/series/:id/providers", h.AddProvider)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token}
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
		{"GET", "/api/library/imports/match?path=x"},
		{"POST", "/api/series/" + uuid.New().String() + "/providers"},
	} {
		rec := env.doUnauth(r.method, r.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauth = %d, want 401", r.method, r.path, rec.Code)
		}
	}
}

// TestLibraryScan_Returns200 proves the happy path: scanning a seeded temp
// storage directory returns 200 with the staged series.
func TestLibraryScan_Returns200(t *testing.T) {
	env := newEnvWithStorageSeeded(t)
	rec := env.do("POST", "/api/library/scan", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("scan = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// TestLibraryImports_ListsStagedEntries proves the ListImports happy path
// end-to-end: a seeded on-disk series is staged by POST /library/scan, then
// GET /library/imports returns it with every field populated. This exercises
// the service's decode(row.Found)→distinctProviders reuse (not just a non-nil
// check) by asserting the recovered Providers list contains the source name.
func TestLibraryImports_ListsStagedEntries(t *testing.T) {
	env := newEnvWithStorageSeeded(t)

	if rec := env.do("POST", "/api/library/scan", ""); rec.Code != http.StatusOK {
		t.Fatalf("scan = %d, want 200 (%s)", rec.Code, rec.Body.String())
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
