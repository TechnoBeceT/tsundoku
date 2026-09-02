// Package server_test — static serving and API-not-found behaviour.
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
	"github.com/technobecet/tsundoku/internal/metrics"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/server"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/bind"
	"github.com/technobecet/tsundoku/internal/tracker/connect"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// nullEngineClient is a stub sourceengine.Client used by route-level tests
// that do not exercise any imports/library paths; it panics if any method is
// called so accidental invocations are immediately obvious in test output.
type nullEngineClient struct{}

func (nullEngineClient) Health(_ context.Context) (sourceengine.Health, error) {
	panic("nullEngineClient.Health called in test")
}
func (nullEngineClient) Status(_ context.Context) (sourceengine.EngineStatus, error) {
	panic("nullEngineClient.Status called in test")
}
func (nullEngineClient) Search(_ context.Context, _ int64, _ string, _ int) (sourceengine.SearchResult, error) {
	panic("nullEngineClient.Search called in test")
}
func (nullEngineClient) Popular(_ context.Context, _ int64, _ int) (sourceengine.SearchResult, error) {
	panic("nullEngineClient.Popular called in test")
}
func (nullEngineClient) Latest(_ context.Context, _ int64, _ int) (sourceengine.SearchResult, error) {
	panic("nullEngineClient.Latest called in test")
}
func (nullEngineClient) MangaDetails(_ context.Context, _ int64, _ string) (sourceengine.MangaDetails, error) {
	panic("nullEngineClient.MangaDetails called in test")
}
func (nullEngineClient) Chapters(_ context.Context, _ int64, _ string, _ string) ([]sourceengine.Chapter, error) {
	panic("nullEngineClient.Chapters called in test")
}
func (nullEngineClient) Pages(_ context.Context, _ int64, _, _ string) ([]sourceengine.Page, error) {
	panic("nullEngineClient.Pages called in test")
}
func (nullEngineClient) Image(_ context.Context, _ int64, _, _ string) ([]byte, string, error) {
	panic("nullEngineClient.Image called in test")
}
func (nullEngineClient) Sources(_ context.Context) ([]sourceengine.Source, error) {
	panic("nullEngineClient.Sources called in test")
}
func (nullEngineClient) Preferences(_ context.Context, _ int64) ([]sourceengine.Preference, error) {
	panic("nullEngineClient.Preferences called in test")
}
func (nullEngineClient) SetPreferences(_ context.Context, _ int64, _ map[string]any) ([]sourceengine.Preference, error) {
	panic("nullEngineClient.SetPreferences called in test")
}
func (nullEngineClient) Extensions(_ context.Context) ([]sourceengine.Extension, error) {
	panic("nullEngineClient.Extensions called in test")
}
func (nullEngineClient) InstallExtension(_ context.Context, _, _ string) ([]sourceengine.Extension, error) {
	panic("nullEngineClient.InstallExtension called in test")
}
func (nullEngineClient) RefreshExtensions(_ context.Context) ([]sourceengine.Extension, error) {
	panic("nullEngineClient.RefreshExtensions called in test")
}
func (nullEngineClient) UpdateExtension(_ context.Context, _ string) ([]sourceengine.Extension, error) {
	panic("nullEngineClient.UpdateExtension called in test")
}
func (nullEngineClient) UninstallExtension(_ context.Context, _ string) ([]sourceengine.Extension, error) {
	panic("nullEngineClient.UninstallExtension called in test")
}
func (nullEngineClient) Repos(_ context.Context) ([]string, error) {
	panic("nullEngineClient.Repos called in test")
}
func (nullEngineClient) SetRepos(_ context.Context, _ []string) ([]string, error) {
	panic("nullEngineClient.SetRepos called in test")
}
func (nullEngineClient) RepoTrust(_ context.Context) (map[string]string, error) {
	panic("nullEngineClient.RepoTrust called in test")
}
func (nullEngineClient) SetRepoTrust(_ context.Context, _, _ string) (map[string]string, error) {
	panic("nullEngineClient.SetRepoTrust called in test")
}
func (nullEngineClient) SetFlareSolverr(_ context.Context, _ sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	panic("nullEngineClient.SetFlareSolverr called in test")
}
func (nullEngineClient) SetSocks(_ context.Context, _ sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	panic("nullEngineClient.SetSocks called in test")
}
func (nullEngineClient) SetImpersonate(_ context.Context, _ sourceengine.ImpersonatePatch) (sourceengine.ImpersonateConfig, error) {
	panic("nullEngineClient.SetImpersonate called in test")
}
func (nullEngineClient) SetImageTransport(_ context.Context, _ sourceengine.ImageTransportPatch) (sourceengine.ImageTransportConfig, error) {
	panic("nullEngineClient.SetImageTransport called in test")
}

// newTestServer builds a server.New instance with stub dependencies and no
// real DB, suitable for route-level unit tests that do not touch the database.
func newTestServer(t *testing.T) (*echo.Echo, *auth.Service) {
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
	ownerH := owner.NewHandler(nil, authSvc, false)

	settingsSvc := settings.NewService(nil, settings.Defaults{})
	metricsSvc := metrics.NewService(nil)
	eventsSvc := sourceevents.NewService(nil)
	warmupSvc := warmup.NewService(nullEngineClient{}, metricsSvc, settingsSvc, nil)
	// No metadata providers wired for these route-level tests — an empty
	// registry never fires an outbound call, matching the nil-DB/panic-on-use
	// discipline the other stubs above follow.
	metaSvc := metadatasvc.NewService(nil, metadata.NewRegistry(), "")
	// No tracker connections wired for these route-level tests either — an
	// empty registry + nil-client connect/bind services never fire an
	// outbound call or DB query, matching the other stubs' discipline.
	trackerRegistry := tracker.NewRegistry()
	trackerConnectSvc := connect.NewService(nil, trackerRegistry, "")
	trackerBindSvc := bind.NewService(nil, trackerRegistry, "")
	// Same nil-client/panic-on-use discipline as the other stubs above — no
	// route-level test in this file exercises the Phase-4c sync endpoints.
	trackerSyncSvc := syncsvc.NewService(nil, trackerRegistry, retry.NewQueue(nil), trackerBindSvc, settingsSvc)
	// The trailing nil is the provider-heal sink (registerProviderHealer): these
	// route-level tests never run a refresh sweep, so no healer is registered.
	return server.New(cfg, nil, authSvc, hub, ownerH, nullEngineClient{}, settingsSvc, nil, nil, nil, nil, nil, metricsSvc, eventsSvc, warmupSvc, nil, nil, metaSvc, trackerRegistry, trackerConnectSvc, trackerBindSvc, trackerSyncSvc, nil, "", func() {}, nil, nil, nil, nil, nil, nil), authSvc
}

// TestSourceConfigurationRoutesAreExactAndOwnerAuthorized catches missing,
// renamed, method-drifted, or publicly mounted source-configuration routes. It
// also pins the existing network-management surface while retiring the old
// binding mutation alias.
func TestSourceConfigurationRoutesAreExactAndOwnerAuthorized(t *testing.T) {
	e, _ := newTestServer(t)

	routes := make(map[string]struct{}, len(e.Routes()))
	for _, route := range e.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	wantRoutes := []string{
		http.MethodGet + " /api/sources/exceptions",
		http.MethodGet + " /api/sources/:sourceId/effective-configuration",
		http.MethodPatch + " /api/sources/:sourceId/transport",
		http.MethodPut + " /api/sources/:sourceId/image-proxy",
		http.MethodGet + " /api/network/endpoints",
		http.MethodPost + " /api/network/endpoints",
		http.MethodPatch + " /api/network/endpoints/:id",
		http.MethodDelete + " /api/network/endpoints/:id",
		http.MethodGet + " /api/network/bindings",
		http.MethodPut + " /api/network/bindings/:sourceId",
		http.MethodDelete + " /api/network/bindings/:sourceId",
	}
	for _, want := range wantRoutes {
		if _, ok := routes[want]; !ok {
			t.Errorf("route table missing %s", want)
		}
	}
	for _, retired := range []string{
		http.MethodPut + " /api/network/sources/:sourceId/binding",
		http.MethodDelete + " /api/network/sources/:sourceId/binding",
	} {
		if _, ok := routes[retired]; ok {
			t.Errorf("route table retains retired alias %s", retired)
		}
	}

	for _, request := range []struct {
		method string
		target string
	}{
		{http.MethodGet, "/api/sources/exceptions"},
		{http.MethodGet, "/api/sources/42/effective-configuration"},
		{http.MethodPatch, "/api/sources/42/transport"},
		{http.MethodPut, "/api/sources/42/image-proxy"},
		{http.MethodPut, "/api/network/bindings/42"},
		{http.MethodDelete, "/api/network/bindings/42"},
	} {
		req := httptest.NewRequest(request.method, request.target, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without owner auth: status = %d, want %d", request.method, request.target, rec.Code, http.StatusUnauthorized)
		}
	}
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
