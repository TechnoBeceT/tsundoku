// Package server_test — static serving and API-not-found behaviour.
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/server"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// nullSuwayomiClient is a stub suwayomi.Client used by route-level tests that
// do not exercise any imports paths; it panics if any method is called so
// accidental invocations are immediately obvious in test output.
type nullSuwayomiClient struct{}

func (nullSuwayomiClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("nullSuwayomiClient.Sources called in test")
}
func (nullSuwayomiClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	panic("nullSuwayomiClient.Search called in test")
}
func (nullSuwayomiClient) Browse(_ context.Context, _ string, _ suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	panic("nullSuwayomiClient.Browse called in test")
}
func (nullSuwayomiClient) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("nullSuwayomiClient.FetchChapters called in test")
}
func (nullSuwayomiClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("nullSuwayomiClient.MangaChapters called in test")
}
func (nullSuwayomiClient) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("nullSuwayomiClient.MangaMeta called in test")
}
func (nullSuwayomiClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	panic("nullSuwayomiClient.ChapterPages called in test")
}
func (nullSuwayomiClient) PageBytes(_ context.Context, _ string) ([]byte, string, error) {
	panic("nullSuwayomiClient.PageBytes called in test")
}

// newTestServer builds a server.New instance with stub dependencies and no
// real DB, suitable for route-level unit tests that do not touch the database.
func newTestServer(t *testing.T) (http.Handler, *auth.Service) {
	t.Helper()
	const secret = "supersecrettestkey1234" // >= 16 chars

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: "9833"},
		Database: config.DatabaseConfig{Password: "x"},
		Auth:     config.AuthConfig{Secret: secret},
	}
	authSvc := auth.NewService(secret)
	hub := sse.NewHub()

	// NewHandler requires a real *ent.Client; for route-level tests we pass nil
	// and ensure no test exercises a path that calls into the DB.
	ownerH := owner.NewHandler(nil, authSvc)

	return server.New(cfg, nil, authSvc, hub, ownerH, nullSuwayomiClient{}, func() {}), authSvc
}

// TestUnknownAPIPathReturns404JSON confirms that an unrecognised /api/* path
// returns 404 with a JSON ErrorResponse, not HTML or an empty body.
func TestUnknownAPIPathReturns404JSON(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/unknown: status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var resp mw.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON ErrorResponse: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Message == "" {
		t.Error("404 response has empty message")
	}
}

// TestNonAPIPathWhenDistAbsent confirms that when the dist/ directory does not
// exist (dev mode) a non-/api path returns a 404 rather than panicking or
// crashing. The SPA is gracefully absent.
func TestNonAPIPathWhenDistAbsent(t *testing.T) {
	// The dist/ directory should not exist in CI/test environments.
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/some-spa-page", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// With no dist/ the SPA fallback is disabled. Echo returns 404 for unmatched
	// routes; that's acceptable — no panic and no 500.
	if rec.Code == http.StatusInternalServerError {
		t.Errorf("GET /some-spa-page without dist: unexpected 500 (body: %s)", rec.Body.String())
	}
}

// TestHealthEndpointViaServer confirms that /health returns 200 after full
// server.New wiring (middleware chain does not break the handler).
func TestHealthEndpointViaServer(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestProgressWithoutBearerReturns401 confirms that /api/progress without a
// Bearer token is rejected with 401 — RequireOwner is wired correctly.
func TestProgressWithoutBearerReturns401(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/progress (no auth): status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp mw.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("401 response is not JSON: %v (body: %s)", err, rec.Body.String())
	}
}
