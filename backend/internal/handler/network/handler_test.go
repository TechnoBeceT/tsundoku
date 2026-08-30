// Package network_test exercises the per-source network-routing HTTP handlers
// end-to-end through a real Echo instance (RequireOwner + the central error
// middleware wired) against an ephemeral PostgreSQL instance (testdb, for the
// real network.Service). Tests require Docker.
package network_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	handler "github.com/technobecet/tsundoku/internal/handler/network"
	configurationhandler "github.com/technobecet/tsundoku/internal/handler/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/middleware"
	networksvc "github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

const testSecret = "network-handler-test-secret-value" //nolint:gosec // test fixture, not a real credential

type testEnv struct {
	e      *echo.Echo
	token  string
	client *ent.Client
}

type acceptingCatalog struct{}

func (acceptingCatalog) RequireSource(context.Context, int64) error { return nil }

type runtimeApplierFunc func(context.Context, int64) error

func (f runtimeApplierFunc) ApplySourceRuntime(ctx context.Context, sourceID int64) error {
	return f(ctx, sourceID)
}

type configurationGetterFunc func(context.Context, int64) (sourceconfiguration.Configuration, error)

func (f configurationGetterFunc) Get(ctx context.Context, sourceID int64) (sourceconfiguration.Configuration, error) {
	return f(ctx, sourceID)
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
		{http.MethodPut, "/api/network/bindings/42"},
		{http.MethodDelete, "/api/network/bindings/42"},
	}
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithApplier(t, runtimeApplierFunc(func(context.Context, int64) error { return nil }))
}

func newTestEnvWithApplier(t *testing.T, applier runtimeApplierFunc) *testEnv {
	return newTestEnvWithCatalog(t, acceptingCatalog{}, applier)
}

func newTestEnvWithCatalog(t *testing.T, catalog sourcetransport.SourceCatalog, applier runtimeApplierFunc) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	networkService := networksvc.NewService(client, catalog)
	runtimeService := sourcetransport.NewService(client, nil, catalog).WithRuntimeApplier(applier)
	reader := configurationhandler.NewDTOReader(configurationGetterFunc(func(ctx context.Context, sourceID int64) (sourceconfiguration.Configuration, error) {
		configuration := sourceconfiguration.Configuration{
			Source:  sourceconfiguration.SourceIdentity{SourceID: sourceID, Name: "Source", Language: "en"},
			Routing: sourceconfiguration.RoutingConfiguration{SocksMode: sourceconfiguration.SocksModeGlobal, BypassMode: networksvc.FlareModeGlobal},
		}
		if binding, err := networkService.GetBinding(ctx, sourceID); err == nil {
			configuration.Routing.BypassMode = binding.FlareMode
		} else if !errors.Is(err, networksvc.ErrBindingNotFound) {
			return sourceconfiguration.Configuration{}, err
		}
		intent, err := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(sourceID)).Only(ctx)
		if err == nil {
			configuration.Runtime = sourceconfiguration.RuntimeStatus{
				DesiredRevision: intent.DesiredRevision, AppliedRevision: intent.AppliedRevision,
				LastApplyAttempt: intent.LastApplyAttempt, LastApplyError: intent.LastApplyError,
			}
			configuration.Runtime.Status = sourceconfiguration.RuntimePending
			if intent.DesiredRevision <= intent.AppliedRevision {
				configuration.Runtime.Status = sourceconfiguration.RuntimeApplied
			}
		} else if !ent.IsNotFound(err) {
			return sourceconfiguration.Configuration{}, err
		} else {
			configuration.Runtime.Status = sourceconfiguration.RuntimeApplied
		}
		return configuration, nil
	}))
	h := handler.NewHandler(networkService, nil).WithSourceRuntime(runtimeService, reader)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/network/endpoints", h.ListEndpoints)
	authed.POST("/network/endpoints", h.CreateEndpoint)
	authed.PATCH("/network/endpoints/:id", h.UpdateEndpoint)
	authed.DELETE("/network/endpoints/:id", h.DeleteEndpoint)
	authed.GET("/network/bindings", h.ListBindings)
	authed.PUT("/network/bindings/:sourceId", h.SetBinding)
	authed.DELETE("/network/bindings/:sourceId", h.ClearBinding)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, token: token, client: client}
}

type rejectingCatalog struct{ err error }

func (c rejectingCatalog) RequireSource(context.Context, int64) error { return c.err }

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

// TestCreateEndpoint_ResponseFallbackDefaultsTrue proves the FlareSolverr
// response-fallback flag defaults to TRUE when the create body omits it (the
// sensible reactive-fallback default the endpoint form relies on) and honours an
// explicit false. This is the load-bearing zero-disruption default.
func TestCreateEndpoint_ResponseFallbackDefaultsTrue(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodPost, "/api/network/endpoints",
		`{"name":"FS default","kind":"flaresolverr","url":"http://flaresolverr:8191"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create (omitted): want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeResponseFallback(t, rec.Body.Bytes()); !got {
		t.Errorf("asResponseFallback omitted → %v, want true", got)
	}

	rec2 := env.do(http.MethodPost, "/api/network/endpoints",
		`{"name":"FS off","kind":"flaresolverr","url":"http://flaresolverr:8191","asResponseFallback":false}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("create (false): want 201, got %d (%s)", rec2.Code, rec2.Body.String())
	}
	if got := decodeResponseFallback(t, rec2.Body.Bytes()); got {
		t.Errorf("asResponseFallback:false → %v, want false", got)
	}
}

// TestCreateEndpoint_TimeoutDefaults proves an omitted FlareSolverr timeout
// defaults to 60 (the ent default) — toInput must apply its computed default,
// not pass the raw zero through — while an explicit value is honoured.
func TestCreateEndpoint_TimeoutDefaults(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodPost, "/api/network/endpoints",
		`{"name":"FS notimeout","kind":"flaresolverr","url":"http://flaresolverr:8191"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create (omitted): want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeTimeout(t, rec.Body.Bytes()); got != 60 {
		t.Errorf("timeout omitted → %d, want 60", got)
	}

	rec2 := env.do(http.MethodPost, "/api/network/endpoints",
		`{"name":"FS t90","kind":"flaresolverr","url":"http://flaresolverr:8191","timeout":90}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("create (explicit): want 201, got %d (%s)", rec2.Code, rec2.Body.String())
	}
	if got := decodeTimeout(t, rec2.Body.Bytes()); got != 90 {
		t.Errorf("timeout:90 → %d, want 90", got)
	}
}

// decodeTimeout pulls the timeout out of an endpoint DTO response body.
func decodeTimeout(t *testing.T, body []byte) int {
	t.Helper()
	var obj struct {
		Timeout int `json:"timeout"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode endpoint DTO: %v", err)
	}
	return obj.Timeout
}

// decodeResponseFallback pulls the asResponseFallback flag out of an endpoint
// DTO response body.
func decodeResponseFallback(t *testing.T, body []byte) bool {
	t.Helper()
	var obj struct {
		AsResponseFallback bool `json:"asResponseFallback"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode endpoint DTO: %v", err)
	}
	return obj.AsResponseFallback
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

	bind := env.do(http.MethodPut, "/api/network/bindings/42",
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
	rec := env.do(http.MethodPut, "/api/network/bindings/42", `{"flareMode":"endpoint"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("endpoint mode without id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetBinding_MalformedSourceID proves a non-numeric sourceId is a 400.
func TestSetBinding_MalformedSourceID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/network/bindings/not-a-number", `{"flareMode":"global"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sourceId: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestBindingMutations_RejectOutOfContractSourceIDGrammar catches accepting
// ParseInt's leading plus or trimming decoded path whitespace before parsing.
func TestBindingMutations_RejectOutOfContractSourceIDGrammar(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "put leading plus", method: http.MethodPut, target: "/api/network/bindings/+42", body: `{"flareMode":"global"}`},
		{name: "put decoded whitespace", method: http.MethodPut, target: "/api/network/bindings/%2042", body: `{"flareMode":"global"}`},
		{name: "delete leading plus", method: http.MethodDelete, target: "/api/network/bindings/+42"},
		{name: "delete decoded whitespace", method: http.MethodDelete, target: "/api/network/bindings/%2042"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			rec := env.do(tc.method, tc.target, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s %s status = %d (%s), want 400", tc.method, tc.target, rec.Code, rec.Body.String())
			}
			if got := env.client.SourceNetworkBinding.Query().CountX(context.Background()); got != 0 {
				t.Fatalf("binding rows after rejected sourceId = %d, want 0", got)
			}
			if got := env.client.SourceRuntimeIntent.Query().CountX(context.Background()); got != 0 {
				t.Fatalf("intent rows after rejected sourceId = %d, want 0", got)
			}
		})
	}
}

// TestSetBinding_RejectsOutOfContractBodies catches permissive framework JSON
// binding: the wire contract is one object with exact known keys and a
// non-null, string flareMode property.
func TestSetBinding_RejectsOutOfContractBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "empty body"},
		{name: "null object", body: `null`},
		{name: "array", body: `[]`},
		{name: "malformed", body: `{`},
		{name: "missing flare mode", body: `{}`},
		{name: "null flare mode", body: `{"flareMode":null}`},
		{name: "wrong flare mode type", body: `{"flareMode":1}`},
		{name: "case changed key", body: `{"FlareMode":"global"}`},
		{name: "unknown key", body: `{"flareMode":"global","unknown":true}`},
		{name: "trailing object", body: `{"flareMode":"global"} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			rec := env.do(http.MethodPut, "/api/network/bindings/42", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %q status = %d (%s), want 400", tc.body, rec.Code, rec.Body.String())
			}
			if got := env.client.SourceNetworkBinding.Query().CountX(context.Background()); got != 0 {
				t.Fatalf("binding rows after rejected body = %d, want 0", got)
			}
			if got := env.client.SourceRuntimeIntent.Query().CountX(context.Background()); got != 0 {
				t.Fatalf("intent rows after rejected body = %d, want 0", got)
			}
		})
	}
}

// TestClearBinding_NotFound proves clearing an unbound source is a 404.
func TestClearBinding_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodDelete, "/api/network/bindings/12345", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("clear unbound: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

type sourceMutationResponse struct {
	Configuration struct {
		Routing struct {
			BypassMode string `json:"bypassMode"`
		} `json:"routing"`
		Runtime struct {
			Status          string `json:"status"`
			DesiredRevision int64  `json:"desiredRevision"`
			AppliedRevision int64  `json:"appliedRevision"`
		} `json:"runtime"`
	} `json:"configuration"`
	Runtime struct {
		Status          string `json:"status"`
		DesiredRevision int64  `json:"desiredRevision"`
		AppliedRevision int64  `json:"appliedRevision"`
		LastApplyError  string `json:"lastApplyError"`
	} `json:"runtime"`
}

func decodeSourceMutation(t *testing.T, rec *httptest.ResponseRecorder) sourceMutationResponse {
	t.Helper()
	var response sourceMutationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode SourceMutationResponse: %v (%s)", err, rec.Body.String())
	}
	return response
}

// TestSetBinding_RuntimeResponseAndNoOpRevision proves the canonical PUT
// returns the shared effective-configuration DTO plus the synchronously applied
// exact revision, while an identical repeat does not create revision churn.
func TestSetBinding_RuntimeResponseAndNoOpRevision(t *testing.T) {
	env := newTestEnv(t)
	for attempt := range 2 {
		rec := env.do(http.MethodPut, "/api/network/bindings/42", `{"flareMode":"none"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT attempt %d status = %d (%s), want 200", attempt+1, rec.Code, rec.Body.String())
		}
		response := decodeSourceMutation(t, rec)
		if response.Configuration.Routing.BypassMode != networksvc.FlareModeNone {
			t.Fatalf("configuration routing = %q, want none", response.Configuration.Routing.BypassMode)
		}
		if response.Runtime.Status != sourceconfiguration.RuntimeApplied || response.Runtime.DesiredRevision != 1 || response.Runtime.AppliedRevision != 1 {
			t.Fatalf("runtime after PUT attempt %d = %+v, want applied 1 / 1", attempt+1, response.Runtime)
		}
	}
}

// TestSetBinding_RuntimeApplyFailureReturnsPersistedPending catches both loss
// of the committed binding and leakage of a raw runtime failure.
func TestSetBinding_RuntimeApplyFailureReturnsPersistedPending(t *testing.T) {
	env := newTestEnvWithApplier(t, runtimeApplierFunc(func(context.Context, int64) error {
		return errors.New("engine unavailable\nretry later")
	}))
	rec := env.do(http.MethodPut, "/api/network/bindings/42", `{"flareMode":"none"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d (%s), want persisted pending 200", rec.Code, rec.Body.String())
	}
	response := decodeSourceMutation(t, rec)
	if response.Configuration.Routing.BypassMode != networksvc.FlareModeNone {
		t.Fatalf("persisted routing = %q, want none", response.Configuration.Routing.BypassMode)
	}
	if response.Runtime.Status != sourceconfiguration.RuntimePending || response.Runtime.DesiredRevision != 1 || response.Runtime.AppliedRevision != 0 || response.Runtime.LastApplyError == "" {
		t.Fatalf("runtime = %+v, want pending 1 / 0 with durable diagnostic", response.Runtime)
	}
	if strings.Contains(response.Runtime.LastApplyError, "\n") || len(response.Runtime.LastApplyError) > 512 {
		t.Fatalf("raw runtime failure leaked: %s", rec.Body.String())
	}
}

// TestClearBinding_RuntimeResponse proves an actual DELETE advances and applies
// its own revision, then returns the effective inherited routing configuration.
func TestClearBinding_RuntimeResponse(t *testing.T) {
	env := newTestEnv(t)
	if rec := env.do(http.MethodPut, "/api/network/bindings/42", `{"flareMode":"none"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := env.do(http.MethodDelete, "/api/network/bindings/42", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	response := decodeSourceMutation(t, rec)
	if response.Configuration.Routing.BypassMode != networksvc.FlareModeGlobal {
		t.Fatalf("routing after delete = %q, want inherited global", response.Configuration.Routing.BypassMode)
	}
	if response.Runtime.Status != sourceconfiguration.RuntimeApplied || response.Runtime.DesiredRevision != 2 || response.Runtime.AppliedRevision != 2 {
		t.Fatalf("runtime after delete = %+v, want applied 2 / 2", response.Runtime)
	}
}

func TestBindingMutation_SourceValidationErrorsStaySanitized(t *testing.T) {
	for _, tc := range []struct {
		err     error
		status  int
		message string
	}{
		{err: errors.Join(sourcetransport.ErrSourceNotFound, errors.New("catalog internals")), status: http.StatusNotFound, message: "source not found"},
		{err: errors.Join(sourcetransport.ErrCatalogUnavailable, errors.New("dial tcp secret-host")), status: http.StatusServiceUnavailable, message: "source catalog unavailable"},
	} {
		env := newTestEnvWithCatalog(t, rejectingCatalog{err: tc.err}, runtimeApplierFunc(func(context.Context, int64) error { return nil }))
		rec := env.do(http.MethodPut, "/api/network/bindings/42", `{"flareMode":"global"}`)
		if rec.Code != tc.status || rec.Body.String() != `{"message":"`+tc.message+`"}`+"\n" {
			t.Fatalf("error %v status=%d body=%s, want %d %q", tc.err, rec.Code, rec.Body.String(), tc.status, tc.message)
		}
		if got := env.client.SourceNetworkBinding.Query().CountX(context.Background()); got != 0 {
			t.Fatalf("binding rows after source validation failure = %d, want 0", got)
		}
		if got := env.client.SourceRuntimeIntent.Query().CountX(context.Background()); got != 0 {
			t.Fatalf("intent rows after source validation failure = %d, want 0", got)
		}
	}
}
