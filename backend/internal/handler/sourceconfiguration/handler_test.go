package sourceconfiguration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	handler "github.com/technobecet/tsundoku/internal/handler/sourceconfiguration"
	proxyhandler "github.com/technobecet/tsundoku/internal/handler/sourceimageproxy"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

const testSecret = "source-configuration-handler-test-secret" //nolint:gosec // test fixture

type fakeService struct {
	configuration  sourceconfiguration.Configuration
	summaries      []sourceconfiguration.Summary
	getErr         error
	exceptionsErr  error
	getCalls       int
	exceptionCalls int
	gotID          int64
}

func (f *fakeService) Get(_ context.Context, sourceID int64) (sourceconfiguration.Configuration, error) {
	f.getCalls++
	f.gotID = sourceID
	return f.configuration, f.getErr
}

func (f *fakeService) Exceptions(context.Context) ([]sourceconfiguration.Summary, error) {
	f.exceptionCalls++
	return f.summaries, f.exceptionsErr
}

type testEnv struct {
	e       *echo.Echo
	token   string
	service *fakeService
}

func newHandlerEnv(t *testing.T) *testEnv {
	t.Helper()
	service := &fakeService{}
	h := handler.NewHandler(service)
	authSvc := auth.NewService(testSecret)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources/exceptions", h.Exceptions)
	authed.GET("/sources/:sourceId/effective-configuration", h.Effective)
	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, token: token, service: service}
}

func (env *testEnv) get(target string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

func TestSourceConfigurationRoutesRequireOwner(t *testing.T) {
	for _, target := range []string{
		"/api/sources/exceptions",
		"/api/sources/42/effective-configuration",
	} {
		env := newHandlerEnv(t)
		rec := env.get(target, false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s status = %d (%s), want 401", target, rec.Code, rec.Body.String())
		}
		if env.service.getCalls != 0 || env.service.exceptionCalls != 0 {
			t.Errorf("GET %s reached service: get=%d exceptions=%d", target, env.service.getCalls, env.service.exceptionCalls)
		}
	}
}

func TestEffectiveConfigurationUsesExactSignedDecimalInt64Grammar(t *testing.T) {
	for _, target := range []string{
		"/api/sources/+42/effective-configuration",
		"/api/sources/%2042%20/effective-configuration",
		"/api/sources/nope/effective-configuration",
		"/api/sources/999999999999999999999/effective-configuration",
	} {
		env := newHandlerEnv(t)
		rec := env.get(target, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d (%s), want 400", target, rec.Code, rec.Body.String())
		}
		if env.service.getCalls != 0 {
			t.Errorf("GET %s reached service with id %d", target, env.service.gotID)
		}
	}

	env := newHandlerEnv(t)
	rec := env.get("/api/sources/-9223372036854775808/effective-configuration", true)
	if rec.Code != http.StatusOK || env.service.gotID != -9223372036854775808 {
		t.Fatalf("minimum int64 status=%d id=%d body=%s", rec.Code, env.service.gotID, rec.Body.String())
	}
}

func TestEffectiveConfigurationMapsUnavailableStoredRoutingExactly(t *testing.T) {
	env := newHandlerEnv(t)
	concurrency := 7
	delay := time.Duration(0)
	reuse := false
	mode := sourcetransport.ImageConnectionReuse
	socksID, socksName := "disabled-socks-id", "Disabled VPN SOCKS"
	flareID := "missing-flare-id"
	attempt := time.Date(2026, 8, 30, 12, 30, 0, 0, time.UTC)
	env.service.configuration = sourceconfiguration.Configuration{
		Source:              sourceconfiguration.SourceIdentity{SourceID: 1998416842837112832, Name: "Large", Language: "en"},
		DownloadConcurrency: sourceconfiguration.IntegerPolicyValue{Override: &concurrency, Effective: 7},
		ImageRequestDelay:   sourceconfiguration.DurationPolicyValue{Override: &delay, Effective: 0},
		Protection: sourceconfiguration.ProtectionConfiguration{
			WarmupInterval: 15 * time.Minute, WarmupSlowThresholdMs: 1250, FailureThreshold: 4,
			SourceCooldown: 20 * time.Minute, PolitenessDelay: 750 * time.Millisecond,
		},
		BypassEnabled: true,
		ReuseBypassSession: sourceconfiguration.BypassSessionPolicyValue{
			Override: &reuse, Global: true, Effective: false, Mode: sourcetransport.BypassSessionDisposable,
		},
		ImageConnectionMode: sourceconfiguration.ImageConnectionPolicyValue{Override: &mode, Global: sourcetransport.ImageConnectionFresh, Effective: mode},
		ImageProxy: sourceconfiguration.ImageProxyState{
			OptedIn: true, GatewayEnabled: true, GatewayConfigured: true, EffectiveAvailable: true,
		},
		Routing: sourceconfiguration.RoutingConfiguration{
			Stored: sourceconfiguration.StoredRoutingConfiguration{
				Configured: true,
				SocksMode:  sourceconfiguration.SocksModeEndpoint,
				Socks:      sourceconfiguration.ResolvedEndpoint{EndpointID: &socksID, Name: &socksName},
				BypassMode: "endpoint",
				Bypass:     sourceconfiguration.ResolvedEndpoint{EndpointID: &flareID},
			},
			SocksMode:  sourceconfiguration.SocksModeGlobal,
			BypassMode: "global",
		},
		ProfileKey: "profile-key",
		Runtime: sourceconfiguration.RuntimeStatus{
			Status: sourceconfiguration.RuntimePending, DesiredRevision: 5, AppliedRevision: 4,
			LastApplyAttempt: &attempt, LastApplyError: "engine unavailable",
		},
	}

	rec := env.get("/api/sources/1998416842837112832/effective-configuration", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	want := `{"source":{"sourceId":"1998416842837112832","name":"Large","language":"en"},"downloadConcurrency":{"override":7,"effective":7,"inherited":false},"imageRequestDelay":{"override":"0s","effective":"0s","inherited":false},"protection":{"warmupInterval":"15m0s","warmupSlowThresholdMs":1250,"failureThreshold":4,"sourceCooldown":"20m0s","politenessDelay":"750ms"},"bypassEnabled":true,"reuseBypassSession":{"override":false,"global":true,"effective":false,"inherited":false,"mode":"disposable"},"imageConnectionMode":{"override":"reuse","global":"fresh","effective":"reuse","inherited":false},"imageProxy":{"optedIn":true,"gatewayEnabled":true,"gatewayConfigured":true,"effectiveAvailable":true},"routing":{"stored":{"configured":true,"socksMode":"endpoint","socks":{"endpointId":"disabled-socks-id","name":"Disabled VPN SOCKS"},"bypassMode":"endpoint","bypass":{"endpointId":"missing-flare-id","name":null}},"socksMode":"global","socks":{"endpointId":null,"name":null},"bypassMode":"global","bypass":{"endpointId":null,"name":null}},"profileKey":"profile-key","runtime":{"status":"pending","desiredRevision":5,"appliedRevision":4,"lastApplyAttempt":"2026-08-30T12:30:00Z","lastApplyError":"engine unavailable"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("response = %s\nwant     = %s", got, want)
	}
}

func TestExceptionsMapsFrozenDTOAndUsesEmptyArray(t *testing.T) {
	env := newHandlerEnv(t)
	env.service.summaries = []sourceconfiguration.Summary{{
		Source:         sourceconfiguration.SourceIdentity{SourceID: -42, Name: "Negative", Language: "tr"},
		ExceptionCount: 3,
		Runtime:        sourceconfiguration.RuntimeStatus{Status: sourceconfiguration.RuntimeApplied, DesiredRevision: 2, AppliedRevision: 2},
	}}
	rec := env.get("/api/sources/exceptions", true)
	if got, want := rec.Body.String(), `[{"source":{"sourceId":"-42","name":"Negative","language":"tr"},"exceptionCount":3,"runtime":{"status":"applied","desiredRevision":2,"appliedRevision":2,"lastApplyAttempt":null,"lastApplyError":""}}]`+"\n"; rec.Code != http.StatusOK || got != want {
		t.Fatalf("status=%d response=%s want=%s", rec.Code, got, want)
	}

	env = newHandlerEnv(t)
	env.service.summaries = nil
	rec = env.get("/api/sources/exceptions", true)
	if got, want := rec.Body.String(), "[]\n"; rec.Code != http.StatusOK || got != want {
		t.Fatalf("empty status=%d response=%q want=%q", rec.Code, got, want)
	}
}

func TestSourceConfigurationMapsFixedErrorsWithoutRawDetails(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{errors.Join(sourceconfiguration.ErrSourceNotFound, errors.New("catalog detail")), http.StatusNotFound, "source not found"},
		{errors.Join(sourceconfiguration.ErrCatalogUnavailable, errors.New("dial tcp secret-host")), http.StatusServiceUnavailable, "source catalog unavailable"},
	}
	for _, tc := range cases {
		env := newHandlerEnv(t)
		env.service.getErr = tc.err
		rec := env.get("/api/sources/42/effective-configuration", true)
		if rec.Code != tc.status || rec.Body.String() != `{"message":"`+tc.message+`"}`+"\n" {
			t.Errorf("effective err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}

		env = newHandlerEnv(t)
		env.service.exceptionsErr = tc.err
		rec = env.get("/api/sources/exceptions", true)
		if rec.Code != tc.status || rec.Body.String() != `{"message":"`+tc.message+`"}`+"\n" {
			t.Errorf("exceptions err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
	}
}

func TestSourceConfigurationUnexpectedErrorsAreSanitized(t *testing.T) {
	env := newHandlerEnv(t)
	env.service.getErr = errors.New("postgres password=hunter2")
	rec := env.get("/api/sources/42/effective-configuration", true)
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"message":"internal server error"}`+"\n" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatal("raw service error leaked")
	}
}

func TestDTOReaderSatisfiesImageProxyConfigurationComposerSeam(t *testing.T) {
	service := &fakeService{configuration: sourceconfiguration.Configuration{Source: sourceconfiguration.SourceIdentity{SourceID: 42}}}
	reader := handler.NewDTOReader(service)
	var _ proxyhandler.ConfigurationReader[handler.ConfigurationDTO] = reader
	got, err := reader.Get(context.Background(), 42)
	if err != nil || got.Source.SourceID != "42" {
		t.Fatalf("reader Get = %+v, %v", got, err)
	}
}
