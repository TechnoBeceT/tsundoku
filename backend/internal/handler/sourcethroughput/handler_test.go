package sourcethroughput_test

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

	handler "github.com/technobecet/tsundoku/internal/handler/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
)

const testSecret = "source-throughput-handler-test-secret" //nolint:gosec // test fixture

type fakeService struct {
	defaults sourcethroughput.Effective
	snapshot map[int64]sourcethroughput.Override
	updated  sourcethroughput.Override
	err      error
	gotID    int64
	gotPatch sourcethroughput.Patch
}

func (f *fakeService) Snapshot(context.Context) (map[int64]sourcethroughput.Override, error) {
	return f.snapshot, f.err
}

func (f *fakeService) Defaults(context.Context) sourcethroughput.Effective { return f.defaults }

func (f *fakeService) Update(_ context.Context, id int64, patch sourcethroughput.Patch) (sourcethroughput.Override, error) {
	f.gotID, f.gotPatch = id, patch
	return f.updated, f.err
}

type testEnv struct {
	e     *echo.Echo
	token string
	svc   *fakeService
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	svc := &fakeService{defaults: sourcethroughput.Effective{DownloadConcurrency: 5, ImageRequestDelay: 500 * time.Millisecond}}
	h := handler.NewHandler(svc)
	authSvc := auth.NewService(testSecret)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources/throughput", h.List)
	authed.PATCH("/sources/:sourceId/throughput", h.Update)
	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, token: token, svc: svc}
}

func (env *testEnv) do(method, target, body string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

func TestRoutesRequireOwner(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/api/sources/throughput"},
		{http.MethodPatch, "/api/sources/42/throughput"},
	} {
		rec := env.do(tc.method, tc.target, `{}`, false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", tc.method, tc.target, rec.Code)
		}
	}
}

func TestListReturnsDefaultsAndSortedEffectivePolicies(t *testing.T) {
	env := newTestEnv(t)
	one := 1
	zero := time.Duration(0)
	env.svc.snapshot = map[int64]sourcethroughput.Override{
		99: {ImageRequestDelay: &zero},
		42: {DownloadConcurrency: &one},
	}
	rec := env.do(http.MethodGet, "/api/sources/throughput", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("List: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	want := `{"defaults":{"downloadConcurrency":5,"imageRequestDelay":"500ms"},"sources":[{"sourceId":42,"downloadConcurrency":{"override":1,"effective":1},"imageRequestDelay":{"override":null,"effective":"500ms"}},{"sourceId":99,"downloadConcurrency":{"override":null,"effective":5},"imageRequestDelay":{"override":"0s","effective":"0s"}}]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("List body:\n got %s\nwant %s", got, want)
	}
}

func TestUpdateMapsKeepSetClearAndReturnsRefreshedEffectivePolicy(t *testing.T) {
	env := newTestEnv(t)
	zero := time.Duration(0)
	env.svc.updated = sourcethroughput.Override{ImageRequestDelay: &zero}
	rec := env.do(http.MethodPatch, "/api/sources/-42/throughput", `{"downloadConcurrency":{"mode":"inherit"},"imageRequestDelay":{"mode":"override","value":"0s"}}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if env.svc.gotID != -42 || env.svc.gotPatch.DownloadConcurrency.Operation != sourcethroughput.PatchClear || env.svc.gotPatch.ImageRequestDelay.Operation != sourcethroughput.PatchSet || env.svc.gotPatch.ImageRequestDelay.Value != 0 {
		t.Fatalf("service call = id %d patch %#v", env.svc.gotID, env.svc.gotPatch)
	}
	want := `{"sourceId":-42,"downloadConcurrency":{"override":null,"effective":5},"imageRequestDelay":{"override":"0s","effective":"0s"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("Update body:\n got %s\nwant %s", got, want)
	}
}

func TestUpdateKeepsOmittedField(t *testing.T) {
	env := newTestEnv(t)
	one := 1
	env.svc.updated = sourcethroughput.Override{DownloadConcurrency: &one}
	rec := env.do(http.MethodPatch, "/api/sources/7/throughput", `{"downloadConcurrency":{"mode":"override","value":1}}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if env.svc.gotPatch.ImageRequestDelay.Operation != sourcethroughput.PatchKeep {
		t.Fatalf("omitted image delay operation = %d, want keep", env.svc.gotPatch.ImageRequestDelay.Operation)
	}
}

func TestUpdateRejectsMalformedSourceIDAndEmptyPatch(t *testing.T) {
	for _, tc := range []struct{ target, body string }{
		{"/api/sources/nope/throughput", `{"downloadConcurrency":{"mode":"inherit"}}`},
		{"/api/sources/999999999999999999999/throughput", `{"downloadConcurrency":{"mode":"inherit"}}`},
		{"/api/sources/42/throughput", `{}`},
	} {
		env := newTestEnv(t)
		rec := env.do(http.MethodPatch, tc.target, tc.body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s body %s: got %d (%s), want 400", tc.target, tc.body, rec.Code, rec.Body.String())
		}
	}
}

func TestUpdateRejectsInvalidFieldEncodings(t *testing.T) {
	cases := []string{
		`{"downloadConcurrency":{"mode":"other","value":1}}`,
		`{"downloadConcurrency":{"mode":"inherit","value":1}}`,
		`{"downloadConcurrency":{"mode":"inherit","value":null}}`,
		`{"downloadConcurrency":{"mode":"override"}}`,
		`{"downloadConcurrency":{"mode":"override","value":null}}`,
		`{"downloadConcurrency":{"mode":"override","value":0}}`,
		`{"downloadConcurrency":{"mode":"override","value":33}}`,
		`{"imageRequestDelay":{"mode":"inherit","value":"1s"}}`,
		`{"imageRequestDelay":{"mode":"inherit","value":null}}`,
		`{"imageRequestDelay":{"mode":"override"}}`,
		`{"imageRequestDelay":{"mode":"override","value":null}}`,
		`{"imageRequestDelay":{"mode":"override","value":"bad"}}`,
		`{"imageRequestDelay":{"mode":"override","value":"-1ms"}}`,
		`{"imageRequestDelay":{"mode":"override","value":"500us"}}`,
	}
	for _, body := range cases {
		env := newTestEnv(t)
		rec := env.do(http.MethodPatch, "/api/sources/42/throughput", body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: got %d (%s), want 400", body, rec.Code, rec.Body.String())
		}
	}
}

func TestServiceErrorsReachCentralErrorHandler(t *testing.T) {
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPatch, `{"downloadConcurrency":{"mode":"inherit"}}`},
	} {
		env := newTestEnv(t)
		env.svc.err = errors.New("database unavailable")
		target := "/api/sources/throughput"
		if tc.method == http.MethodPatch {
			target = "/api/sources/42/throughput"
		}
		rec := env.do(tc.method, target, tc.body, true)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: got %d (%s), want 500", tc.method, rec.Code, rec.Body.String())
		}
	}
}
