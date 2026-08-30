package sourceimageproxy_test

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

	handler "github.com/technobecet/tsundoku/internal/handler/sourceimageproxy"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceimageproxy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

const testSecret = "source-image-proxy-handler-test-secret" //nolint:gosec // test fixture

type fakeUpdater struct {
	result  sourceimageproxy.UpdateResult
	err     error
	called  bool
	gotID   int64
	enabled bool
}

func (f *fakeUpdater) Update(_ context.Context, sourceID int64, enabled bool) (sourceimageproxy.UpdateResult, error) {
	f.called, f.gotID, f.enabled = true, sourceID, enabled
	return f.result, f.err
}

type fakeApplier struct {
	intent sourcetransport.Intent
	err    error
	gotID  int64
}

func (f *fakeApplier) ApplyPending(_ context.Context, sourceID int64) (sourcetransport.Intent, error) {
	f.gotID = sourceID
	return f.intent, f.err
}

type testConfiguration struct {
	Marker string `json:"marker"`
}

type fakeConfigurationReader struct {
	configuration testConfiguration
	err           error
	gotID         int64
}

func (f *fakeConfigurationReader) Get(_ context.Context, sourceID int64) (testConfiguration, error) {
	f.gotID = sourceID
	return f.configuration, f.err
}

type testEnv struct {
	e       *echo.Echo
	token   string
	updater *fakeUpdater
	applier *fakeApplier
	reader  *fakeConfigurationReader
}

func newHandlerEnv(t *testing.T) *testEnv {
	t.Helper()
	updater := &fakeUpdater{}
	applier := &fakeApplier{}
	reader := &fakeConfigurationReader{configuration: testConfiguration{Marker: "fresh"}}
	h := handler.NewHandler(updater, applier, reader)
	authSvc := auth.NewService(testSecret)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.PUT("/sources/:sourceId/image-proxy", h.Update)
	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, token: token, updater: updater, applier: applier, reader: reader}
}

func (env *testEnv) do(target, body string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

func TestImageProxyRouteRequiresOwner(t *testing.T) {
	env := newHandlerEnv(t)
	rec := env.do("/api/sources/42/image-proxy", `{"enabled":true}`, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d (%s), want 401", rec.Code, rec.Body.String())
	}
	if env.updater.called {
		t.Fatal("unauthorized request reached service")
	}
}

func TestImageProxyUpdateRejectsMalformedBoundaryInput(t *testing.T) {
	cases := []struct {
		target string
		body   string
	}{
		{"/api/sources/nope/image-proxy", `{"enabled":true}`},
		{"/api/sources/999999999999999999999/image-proxy", `{"enabled":true}`},
		{"/api/sources/42/image-proxy", `{}`},
		{"/api/sources/42/image-proxy", `{"enabled":null}`},
		{"/api/sources/42/image-proxy", `{"enabled":true,"unknown":1}`},
		{"/api/sources/42/image-proxy", `{"enabled":true} {}`},
	}
	for _, tc := range cases {
		env := newHandlerEnv(t)
		rec := env.do(tc.target, tc.body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s body %s: status = %d (%s), want 400", tc.target, tc.body, rec.Code, rec.Body.String())
		}
		if env.updater.called {
			t.Errorf("PUT %s body %s reached service", tc.target, tc.body)
		}
	}
}

func TestImageProxyUpdateMapsSourceAndCatalogErrorsWithoutLeakingDetail(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{fmtWrap(sourceimageproxy.ErrSourceNotFound, "catalog detail"), http.StatusNotFound, "source not found"},
		{fmtWrap(sourceimageproxy.ErrCatalogUnavailable, "dial tcp 10.0.1.15: secret"), http.StatusServiceUnavailable, "source catalog unavailable"},
	}
	for _, tc := range cases {
		env := newHandlerEnv(t)
		env.updater.err = tc.err
		rec := env.do("/api/sources/42/image-proxy", `{"enabled":true}`, true)
		if rec.Code != tc.status {
			t.Fatalf("error %v: status = %d (%s), want %d", tc.err, rec.Code, rec.Body.String(), tc.status)
		}
		if got, want := rec.Body.String(), `{"message":"`+tc.message+`"}`+"\n"; got != want {
			t.Fatalf("error body = %q, want %q", got, want)
		}
	}
}

func TestImageProxyUpdateReturnsAppliedRuntimeAndPreservesInt64Boundary(t *testing.T) {
	env := newHandlerEnv(t)
	const sourceID = int64(1998416842837112832)
	env.updater.result = sourceimageproxy.UpdateResult{Enabled: true, SourceIDs: []int64{sourceID}, Intent: sourcetransport.Intent{SourceID: sourceID, DesiredRevision: 3, AppliedRevision: 2}}
	env.applier.intent = sourcetransport.Intent{SourceID: sourceID, DesiredRevision: 3, AppliedRevision: 3}

	rec := env.do("/api/sources/1998416842837112832/image-proxy", `{"enabled":true}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if env.updater.gotID != sourceID || !env.updater.enabled || env.applier.gotID != sourceID || env.reader.gotID != sourceID {
		t.Fatalf("boundary calls: update id=%d enabled=%t apply id=%d read id=%d", env.updater.gotID, env.updater.enabled, env.applier.gotID, env.reader.gotID)
	}
	want := `{"configuration":{"marker":"fresh"},"runtime":{"status":"applied","desiredRevision":3,"appliedRevision":3,"lastApplyAttempt":null,"lastApplyError":""}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("response = %s, want %s", got, want)
	}
}

func TestImageProxyUpdateReturnsPendingAfterRuntimeApplyFailure(t *testing.T) {
	env := newHandlerEnv(t)
	attempt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	env.updater.result = sourceimageproxy.UpdateResult{Enabled: true, Intent: sourcetransport.Intent{SourceID: -42, DesiredRevision: 4, AppliedRevision: 3}}
	env.applier.intent = sourcetransport.Intent{SourceID: -42, DesiredRevision: 4, AppliedRevision: 3, LastApplyAttempt: &attempt, LastApplyError: "engine unavailable"}
	env.applier.err = errors.New("raw apply failure with credentials")

	rec := env.do("/api/sources/-42/image-proxy", `{"enabled":true}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update status = %d (%s), want persisted pending 200", rec.Code, rec.Body.String())
	}
	want := `{"configuration":{"marker":"fresh"},"runtime":{"status":"pending","desiredRevision":4,"appliedRevision":3,"lastApplyAttempt":"2026-08-30T12:00:00Z","lastApplyError":"engine unavailable"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("response = %s, want %s", got, want)
	}
	if strings.Contains(rec.Body.String(), "credentials") {
		t.Fatal("raw runtime apply error leaked")
	}
}

func TestImageProxyUnexpectedServiceErrorIsSanitized(t *testing.T) {
	env := newHandlerEnv(t)
	env.updater.err = errors.New("postgres password=hunter2")
	rec := env.do("/api/sources/42/image-proxy", `{"enabled":false}`, true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"message":"internal server error"}`+"\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func fmtWrap(sentinel error, detail string) error {
	return errors.Join(sentinel, errors.New(detail))
}
