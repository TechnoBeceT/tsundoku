// Package engine_test exercises the internal engine-topology APK-serving handler
// end-to-end through a real Echo instance (RequireOwner + the central error
// middleware wired) against a real on-disk apkcache.Store in a t.TempDir(). No
// Suwayomi or DB is needed.
package engine_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	handler "github.com/technobecet/tsundoku/internal/handler/engine"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

const testSecret = "engine-handler-test-secret"

type testEnv struct {
	e     *echo.Echo
	cache *apkcache.Store
	token string
}

// newTestEnv wires an Echo instance with the /internal group behind RequireOwner
// and an apkcache.Store rooted at a fresh temp dir.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	cache := apkcache.New(t.TempDir())
	// db is nil: the apk-serving route never reads it (only TopologyStatus does),
	// so these apk tests need no DB.
	h := handler.NewHandler(cache, nil)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	internalG := e.Group("/internal", middleware.RequireOwner(authSvc, false))
	internalG.GET("/extensions/apk/:pkg/:file", h.ServeAPK)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, cache: cache, token: token}
}

func (env *testEnv) do(target string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if auth {
		r.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// TestServeAPK_OK proves a cached apk streams back with 200, the Android-package
// content type, and the exact bytes.
func TestServeAPK_OK(t *testing.T) {
	env := newTestEnv(t)
	apk := []byte("cached apk payload \x00\x01")
	if _, _, err := env.cache.Put("pkg.one", 5, bytes.NewReader(apk)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rec := env.do("/internal/extensions/apk/pkg.one/pkg.one-5.apk", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.android.package-archive" {
		t.Errorf("Content-Type = %q, want application/vnd.android.package-archive", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), apk) {
		t.Errorf("body = %q, want %q", rec.Body.Bytes(), apk)
	}
}

// TestServeAPK_NotCached proves an uncached (pkg, version) is a 404.
func TestServeAPK_NotCached(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do("/internal/extensions/apk/pkg.missing/pkg.missing-9.apk", true)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestServeAPK_BadFile proves a filename that is not "<pkg>-<version>.apk" is a
// 400: a non-integer version, a missing .apk suffix, and a filename whose pkg
// prefix does not match the :pkg segment are all rejected (the last guards
// against serving the wrong extension).
func TestServeAPK_BadFile(t *testing.T) {
	env := newTestEnv(t)
	for _, file := range []string{
		"pkg.one-not-a-number.apk", // version not an int
		"pkg.one-5",                // no .apk suffix
		"pkg.other-5.apk",          // pkg prefix mismatches :pkg
	} {
		rec := env.do("/internal/extensions/apk/pkg.one/"+file, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("file %q: status = %d, want 400", file, rec.Code)
		}
	}
}

// TestServeAPK_Unauthorized proves the endpoint is behind RequireOwner: no token
// is a 401, even for a cached apk.
func TestServeAPK_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	if _, _, err := env.cache.Put("pkg.one", 5, bytes.NewReader([]byte("apk"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rec := env.do("/internal/extensions/apk/pkg.one/pkg.one-5.apk", false)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
