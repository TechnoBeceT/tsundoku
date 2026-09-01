package sourcetransport_test

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

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	"github.com/technobecet/tsundoku/internal/ent/sourcetransportpolicy"
	configurationhandler "github.com/technobecet/tsundoku/internal/handler/sourceconfiguration"
	handler "github.com/technobecet/tsundoku/internal/handler/sourcetransport"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

const testSecret = "source-transport-handler-test-secret" //nolint:gosec // test fixture

type fakeUpdater struct {
	result sourcetransport.UpdateResult
	err    error
	called bool
	gotID  int64
	patch  sourcetransport.Patch
}

func (f *fakeUpdater) Update(_ context.Context, sourceID int64, patch sourcetransport.Patch) (sourcetransport.UpdateResult, error) {
	f.called, f.gotID, f.patch = true, sourceID, patch
	return f.result, f.err
}

type fakeApplier struct {
	intent sourcetransport.Intent
	err    error
	calls  int
	gotID  int64
}

func (f *fakeApplier) ApplyPending(_ context.Context, sourceID int64) (sourcetransport.Intent, error) {
	f.calls++
	f.gotID = sourceID
	return f.intent, f.err
}

type testConfiguration struct {
	Marker string `json:"marker"`
}

type fakeConfigurationReader struct {
	configuration testConfiguration
	err           error
	calls         int
	gotID         int64
}

func (f *fakeConfigurationReader) Get(_ context.Context, sourceID int64) (testConfiguration, error) {
	f.calls++
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
	reader := &fakeConfigurationReader{configuration: testConfiguration{Marker: "composed"}}
	e, token := mountHandler(t, handler.NewHandler(updater, applier, reader))
	return &testEnv{e: e, token: token, updater: updater, applier: applier, reader: reader}
}

type transportHandler interface {
	Update(echo.Context) error
}

func mountHandler(t *testing.T, h transportHandler) (*echo.Echo, string) {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	e.Group("/api", middleware.RequireOwner(authSvc, false)).PATCH("/sources/:sourceId/transport", h.Update)
	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return e, token
}

func doPatch(e *echo.Echo, token, target, body string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestTransportRouteRequiresOwner(t *testing.T) {
	env := newHandlerEnv(t)
	rec := doPatch(env.e, env.token, "/api/sources/42/transport", `{}`, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d (%s), want 401", rec.Code, rec.Body.String())
	}
	if env.updater.called {
		t.Fatal("unauthorized request reached service")
	}
}

func TestTransportPatchPreservesOmittedOverrideAndInheritStates(t *testing.T) {
	cases := []struct {
		name string
		body string
		want sourcetransport.Patch
	}{
		{name: "both omitted", body: `{}`, want: sourcetransport.Patch{}},
		{name: "reuse false override", body: `{"reuseBypassSession":{"mode":"override","value":false}}`, want: sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}},
		{name: "reuse true override", body: `{"reuseBypassSession":{"mode":"override","value":true}}`, want: sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(true)}},
		{name: "reuse inherit", body: `{"reuseBypassSession":{"mode":"inherit"}}`, want: sourcetransport.Patch{ReuseBypassSession: sourcetransport.Clear[bool]()}},
		{name: "image override", body: `{"imageConnectionMode":{"mode":"override","value":"reuse"}}`, want: sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)}},
		{name: "image inherit", body: `{"imageConnectionMode":{"mode":"inherit"}}`, want: sourcetransport.Patch{ImageConnectionMode: sourcetransport.Clear[sourcetransport.ImageConnectionMode]()}},
		{name: "embedded browser override", body: `{"kcefPolicy":{"mode":"override","value":"required"}}`, want: sourcetransport.Patch{KCEFPolicy: sourcetransport.Set(runtimepolicy.KCEFPolicyRequired)}},
		{name: "embedded browser inherit", body: `{"kcefPolicy":{"mode":"inherit"}}`, want: sourcetransport.Patch{KCEFPolicy: sourcetransport.Clear[runtimepolicy.KCEFPolicy]()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newHandlerEnv(t)
			rec := doPatch(env.e, env.token, "/api/sources/-42/transport", tc.body, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
			}
			if env.updater.gotID != -42 || env.updater.patch != tc.want {
				t.Fatalf("Update id=%d patch=%+v, want id=-42 patch=%+v", env.updater.gotID, env.updater.patch, tc.want)
			}
		})
	}
}

func TestTransportPatchRejectsMalformedBoundaryInput(t *testing.T) {
	cases := []struct {
		target string
		body   string
	}{
		{target: "/api/sources/+42/transport", body: `{}`},
		{target: "/api/sources/%2042%20/transport", body: `{}`},
		{target: "/api/sources/nope/transport", body: `{}`},
		{target: "/api/sources/999999999999999999999/transport", body: `{}`},
		{target: "/api/sources/42/transport", body: ``},
		{target: "/api/sources/42/transport", body: `null`},
		{target: "/api/sources/42/transport", body: `{"unknown":1}`},
		{target: "/api/sources/42/transport", body: `{"ReuseBypassSession":{"mode":"inherit"}}`},
		{target: "/api/sources/42/transport", body: `{"reuseBypassSession":null}`},
		{target: "/api/sources/42/transport", body: `{"reuseBypassSession":{"mode":"unsupported"}}`},
		{target: "/api/sources/42/transport", body: `{"reuseBypassSession":{"mode":"override"}}`},
		{target: "/api/sources/42/transport", body: `{"reuseBypassSession":{"mode":"inherit","value":true}}`},
		{target: "/api/sources/42/transport", body: `{"reuseBypassSession":{"Mode":"inherit"}}`},
		{target: "/api/sources/42/transport", body: `{"imageConnectionMode":{"mode":"override","value":"pool"}}`},
		{target: "/api/sources/42/transport", body: `{"imageConnectionMode":{"mode":"override"}}`},
		{target: "/api/sources/42/transport", body: `{"imageConnectionMode":{"mode":"inherit","value":"fresh"}}`},
		{target: "/api/sources/42/transport", body: `{"imageConnectionMode":{"mode":"inherit","unknown":1}}`},
		{target: "/api/sources/42/transport", body: `{"kcefPolicy":{"mode":"override","value":"always"}}`},
		{target: "/api/sources/42/transport", body: `{"kcefPolicy":{"mode":"inherit","value":"auto"}}`},
		{target: "/api/sources/42/transport", body: `{} {}`},
	}
	for _, tc := range cases {
		env := newHandlerEnv(t)
		rec := doPatch(env.e, env.token, tc.target, tc.body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s body %q status = %d (%s), want 400", tc.target, tc.body, rec.Code, rec.Body.String())
		}
		if env.updater.called {
			t.Errorf("PATCH %s body %q reached service", tc.target, tc.body)
		}
	}
}

func TestTransportPatchAppliesAndReportsOnlyItsExactCommittedRevision(t *testing.T) {
	env := newHandlerEnv(t)
	const sourceID = int64(1998416842837112832)
	env.updater.result = sourcetransport.UpdateResult{Intent: sourcetransport.Intent{SourceID: sourceID, DesiredRevision: 3, AppliedRevision: 2}}
	env.applier.intent = sourcetransport.Intent{SourceID: sourceID, DesiredRevision: 3, AppliedRevision: 3}

	rec := doPatch(env.e, env.token, "/api/sources/1998416842837112832/transport", `{"imageConnectionMode":{"mode":"override","value":"fresh"}}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if env.applier.calls != 1 || env.applier.gotID != sourceID || env.reader.gotID != sourceID {
		t.Fatalf("apply calls=%d id=%d compose id=%d", env.applier.calls, env.applier.gotID, env.reader.gotID)
	}
	want := `{"configuration":{"marker":"composed"},"runtime":{"status":"applied","desiredRevision":3,"appliedRevision":3,"lastApplyAttempt":null,"lastApplyError":""}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("response = %s, want %s", got, want)
	}

	// A concurrent revision can be what ApplyPending observes. It must not be
	// substituted into this response for the exact revision this request committed.
	env = newHandlerEnv(t)
	env.updater.result = sourcetransport.UpdateResult{Intent: sourcetransport.Intent{SourceID: 42, DesiredRevision: 7, AppliedRevision: 6}}
	env.applier.intent = sourcetransport.Intent{SourceID: 42, DesiredRevision: 8, AppliedRevision: 8}
	rec = doPatch(env.e, env.token, "/api/sources/42/transport", `{}`, true)
	want = `{"configuration":{"marker":"composed"},"runtime":{"status":"pending","desiredRevision":7,"appliedRevision":6,"lastApplyAttempt":null,"lastApplyError":""}}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("newer apply status=%d response=%s want=%s", rec.Code, rec.Body.String(), want)
	}
}

func TestTransportPatchReturnsCommittedPendingStateAfterApplyFailure(t *testing.T) {
	env := newHandlerEnv(t)
	attempt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	env.updater.result = sourcetransport.UpdateResult{Intent: sourcetransport.Intent{SourceID: 42, DesiredRevision: 4, AppliedRevision: 3}}
	env.applier.intent = sourcetransport.Intent{SourceID: 42, DesiredRevision: 4, AppliedRevision: 3, LastApplyAttempt: &attempt, LastApplyError: "fixed diagnostic"}
	env.applier.err = errors.New("raw apply failure with credentials")

	rec := doPatch(env.e, env.token, "/api/sources/42/transport", `{"reuseBypassSession":{"mode":"override","value":false}}`, true)
	want := `{"configuration":{"marker":"composed"},"runtime":{"status":"pending","desiredRevision":4,"appliedRevision":3,"lastApplyAttempt":"2026-08-30T12:00:00Z","lastApplyError":"fixed diagnostic"}}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("status=%d response=%s want=%s", rec.Code, rec.Body.String(), want)
	}
	if strings.Contains(rec.Body.String(), "credentials") {
		t.Fatal("raw runtime apply error leaked")
	}
}

func TestTransportPatchMapsCanonicalErrorsWithoutRawDetails(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{err: errors.Join(sourcetransport.ErrSourceNotFound, errors.New("catalog detail")), status: http.StatusNotFound, message: "source not found"},
		{err: errors.Join(sourcetransport.ErrCatalogUnavailable, errors.New("dial tcp secret-host")), status: http.StatusServiceUnavailable, message: "source catalog unavailable"},
		{err: errors.Join(sourcetransport.ErrInvalidPolicy, errors.New("selected session internal detail")), status: http.StatusBadRequest, message: "invalid source transport policy"},
	}
	for _, tc := range cases {
		env := newHandlerEnv(t)
		env.updater.err = tc.err
		rec := doPatch(env.e, env.token, "/api/sources/42/transport", `{}`, true)
		if rec.Code != tc.status || rec.Body.String() != `{"message":"`+tc.message+`"}`+"\n" {
			t.Errorf("error %v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
		if env.applier.calls != 0 || env.reader.calls != 0 {
			t.Errorf("error %v continued after update: apply=%d compose=%d", tc.err, env.applier.calls, env.reader.calls)
		}
	}
}

type configurationGetterFunc func(context.Context, int64) (sourceconfiguration.Configuration, error)

func (f configurationGetterFunc) Get(ctx context.Context, sourceID int64) (sourceconfiguration.Configuration, error) {
	return f(ctx, sourceID)
}

func TestTransportPostCommitCompositionMapsCanonicalErrors(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{err: errors.Join(sourceconfiguration.ErrSourceNotFound, errors.New("catalog changed")), status: http.StatusNotFound, message: "source not found"},
		{err: errors.Join(sourceconfiguration.ErrCatalogUnavailable, errors.New("dial tcp secret-host")), status: http.StatusServiceUnavailable, message: "source catalog unavailable"},
	}
	for _, tc := range cases {
		updater := &fakeUpdater{result: sourcetransport.UpdateResult{Intent: sourcetransport.Intent{SourceID: 42, DesiredRevision: 1}}}
		applier := &fakeApplier{}
		reader := configurationhandler.NewDTOReader(configurationGetterFunc(func(context.Context, int64) (sourceconfiguration.Configuration, error) {
			return sourceconfiguration.Configuration{}, tc.err
		}))
		e, token := mountHandler(t, handler.NewHandler(updater, applier, reader))
		rec := doPatch(e, token, "/api/sources/42/transport", `{}`, true)
		if !updater.called || applier.calls != 1 {
			t.Fatal("composition error occurred before committed mutation and apply")
		}
		if rec.Code != tc.status || rec.Body.String() != `{"message":"`+tc.message+`"}`+"\n" {
			t.Errorf("error %v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
	}
}

func TestTransportUnexpectedServiceErrorIsSanitized(t *testing.T) {
	env := newHandlerEnv(t)
	env.updater.err = errors.New("postgres password=hunter2")
	rec := doPatch(env.e, env.token, "/api/sources/42/transport", `{}`, true)
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"message":"internal server error"}`+"\n" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type acceptingCatalog struct{}

func (acceptingCatalog) RequireSource(context.Context, int64) error { return nil }

type fixedDefaults struct{}

func (fixedDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return sourcetransport.ImageConnectionFresh
}

func (fixedDefaults) ResolveBypassSession(_ context.Context, _ int64, override *bool) (bool, sourcetransport.BypassSessionMode, error) {
	if override != nil && *override {
		return true, sourcetransport.BypassSessionReusable, nil
	}
	return false, sourcetransport.BypassSessionDisabled, nil
}

type sourceRuntimeApplierFunc func(context.Context, int64) error

func (f sourceRuntimeApplierFunc) ApplySourceRuntime(ctx context.Context, sourceID int64) error {
	return f(ctx, sourceID)
}

func TestTransportServiceApplyFailureReturnsItsCommittedPendingRevision(t *testing.T) {
	client := testdb.New(t)
	applyCalls := 0
	service := sourcetransport.NewService(client, fixedDefaults{}, acceptingCatalog{}).
		WithRuntimeApplier(sourceRuntimeApplierFunc(func(context.Context, int64) error {
			applyCalls++
			return errors.New("engine unavailable")
		}))
	reader := &fakeConfigurationReader{configuration: testConfiguration{Marker: "composed"}}
	e, token := mountHandler(t, handler.NewHandler(service, service, reader))

	rec := doPatch(e, token, "/api/sources/42/transport", `{"imageConnectionMode":{"mode":"override","value":"reuse"}}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want committed pending 200", rec.Code, rec.Body.String())
	}
	if applyCalls != 1 {
		t.Fatalf("runtime apply calls=%d, want exactly one synchronous attempt", applyCalls)
	}
	if strings.Contains(rec.Body.String(), "sourcetransport.Update") {
		t.Fatal("internal service context leaked")
	}
	intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(context.Background())
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 || intent.LastApplyAttempt == nil || intent.LastApplyError == "" {
		t.Fatalf("persisted intent desired=%d applied=%d attempt=%v error=%q, want pending revision 1 with metadata",
			intent.DesiredRevision, intent.AppliedRevision, intent.LastApplyAttempt, intent.LastApplyError)
	}
	want := `{"configuration":{"marker":"composed"},"runtime":{"status":"pending","desiredRevision":1,"appliedRevision":0,"lastApplyAttempt":"` + intent.LastApplyAttempt.Format(time.RFC3339Nano) + `","lastApplyError":"engine unavailable"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("response=%s want=%s", got, want)
	}
}

func TestTransportExplicitReuseRejectsExactlyBlankSessionBeforePersistenceButAcceptsWhitespace(t *testing.T) { //nolint:gocognit // Boundary test compares two complete request outcomes.
	for _, tc := range []struct {
		name       string
		session    string
		wantStatus int
		wantRows   int
	}{
		{name: "exact blank rejected", session: "", wantStatus: http.StatusBadRequest, wantRows: 0},
		{name: "whitespace is configured", session: "   ", wantStatus: http.StatusOK, wantRows: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testdb.New(t)
			service := sourcetransport.NewService(client, fixedDefaults{}, acceptingCatalog{}).
				WithRuntimePolicyCoordinator(runtimepolicy.New(client, tc.session))
			applier := &fakeApplier{}
			reader := &fakeConfigurationReader{configuration: testConfiguration{Marker: "composed"}}
			e, token := mountHandler(t, handler.NewHandler(service, applier, reader))

			rec := doPatch(e, token, "/api/sources/42/transport", `{"reuseBypassSession":{"mode":"override","value":true}}`, true)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s want=%d", rec.Code, rec.Body.String(), tc.wantStatus)
			}
			ctx := context.Background()
			if got := client.SourceTransportPolicy.Query().Where(sourcetransportpolicy.SourceID(42)).CountX(ctx); got != tc.wantRows {
				t.Fatalf("policy rows=%d, want=%d", got, tc.wantRows)
			}
			if got := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).CountX(ctx); got != tc.wantRows {
				t.Fatalf("intent rows=%d, want=%d", got, tc.wantRows)
			}
			if tc.wantRows == 1 {
				policy := client.SourceTransportPolicy.Query().Where(sourcetransportpolicy.SourceID(42)).OnlyX(ctx)
				intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
				if policy.ReuseBypassSession == nil || !*policy.ReuseBypassSession {
					t.Fatalf("persisted reuse override=%v, want true", policy.ReuseBypassSession)
				}
				if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 {
					t.Fatalf("intent desired=%d applied=%d, want 1/0", intent.DesiredRevision, intent.AppliedRevision)
				}
			}
		})
	}
}
