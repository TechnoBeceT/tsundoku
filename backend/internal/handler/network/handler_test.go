// Package network_test exercises the per-source network-routing HTTP handlers
// end-to-end through a real Echo instance (RequireOwner + the central error
// middleware wired) against an ephemeral PostgreSQL instance (testdb, for the
// real network.Service). Tests require Docker.
package network_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/network"
	"github.com/technobecet/tsundoku/internal/middleware"
	networksvc "github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

const testSecret = "network-handler-test-secret-value" //nolint:gosec // test fixture, not a real credential

type testEnv struct {
	e     *echo.Echo
	token string
}

// route is one registered network route, used by the 401 sweep.
type route struct {
	method string
	target string
}

// routes lists every network route so the 401 sweep covers all of them.
func routes() []route {
	return []route{
		{http.MethodGet, "/api/network/endpoints"},
		{http.MethodPost, "/api/network/endpoints"},
		{http.MethodPatch, "/api/network/endpoints/" + uuid.NewString()},
		{http.MethodDelete, "/api/network/endpoints/" + uuid.NewString()},
		{http.MethodGet, "/api/network/bindings"},
		{http.MethodPut, "/api/network/sources/42/binding"},
		{http.MethodDelete, "/api/network/sources/42/binding"},
	}
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(networksvc.NewService(client))

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/network/endpoints", h.ListEndpoints)
	authed.POST("/network/endpoints", h.CreateEndpoint)
	authed.PATCH("/network/endpoints/:id", h.UpdateEndpoint)
	authed.DELETE("/network/endpoints/:id", h.DeleteEndpoint)
	authed.GET("/network/bindings", h.ListBindings)
	authed.PUT("/network/sources/:sourceId/binding", h.SetBinding)
	authed.DELETE("/network/sources/:sourceId/binding", h.ClearBinding)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, token: token}
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

// TestRoutes_Unauthorized proves every network route sits behind RequireOwner.
func TestRoutes_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	for _, rt := range routes() {
		r := httptest.NewRequest(rt.method, rt.target, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: want 401, got %d", rt.method, rt.target, rec.Code)
		}
	}
}

// TestCreateEndpoint_PasswordNeverReturned proves the SOCKS password is
// write-only: it is accepted on create but never echoed by the create response
// or the list.
func TestCreateEndpoint_PasswordNeverReturned(t *testing.T) {
	env := newTestEnv(t)
	body := `{"name":"VPN","kind":"socks","host":"vpn.local","port":1080,"username":"u","password":"top-secret-pw"}`
	rec := env.do(http.MethodPost, "/api/network/endpoints", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateEndpoint: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "top-secret-pw") {
		t.Fatalf("create response leaked the password: %s", rec.Body.String())
	}
	// The DTO must also carry no "password" key at all.
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := obj["password"]; ok {
		t.Errorf("create response contains a password field, want it omitted")
	}

	list := env.do(http.MethodGet, "/api/network/endpoints", "")
	if strings.Contains(list.Body.String(), "top-secret-pw") {
		t.Fatalf("list leaked the password: %s", list.Body.String())
	}
}

// TestCreateEndpoint_InvalidKind proves a bad kind is a 400.
func TestCreateEndpoint_InvalidKind(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/network/endpoints", `{"name":"x","kind":"http"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestUpdateEndpoint_NotFound proves a missing id is a 404.
func TestUpdateEndpoint_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/network/endpoints/"+uuid.NewString(), `{"name":"y"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing id: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestDeleteEndpoint_InUseConflict proves deleting a referenced endpoint is a
// 409 (owner-safety guard).
func TestDeleteEndpoint_InUseConflict(t *testing.T) {
	env := newTestEnv(t)
	create := env.do(http.MethodPost, "/api/network/endpoints", `{"name":"VPN","kind":"socks","host":"vpn.local","port":1080}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	bind := env.do(http.MethodPut, "/api/network/sources/42/binding",
		`{"socksEndpointId":"`+created.ID+`","flareMode":"global"}`)
	if bind.Code != http.StatusOK {
		t.Fatalf("bind: want 200, got %d (%s)", bind.Code, bind.Body.String())
	}

	del := env.do(http.MethodDelete, "/api/network/endpoints/"+created.ID, "")
	if del.Code != http.StatusConflict {
		t.Fatalf("delete referenced: want 409, got %d (%s)", del.Code, del.Body.String())
	}
}

// TestSetBinding_InvalidMode proves the flare_mode consistency rule maps to a
// 400 at the HTTP layer.
func TestSetBinding_InvalidMode(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/network/sources/42/binding", `{"flareMode":"endpoint"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("endpoint mode without id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetBinding_MalformedSourceID proves a non-numeric sourceId is a 400.
func TestSetBinding_MalformedSourceID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/network/sources/not-a-number/binding", `{"flareMode":"global"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sourceId: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestClearBinding_NotFound proves clearing an unbound source is a 404.
func TestClearBinding_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodDelete, "/api/network/sources/12345/binding", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("clear unbound: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}
