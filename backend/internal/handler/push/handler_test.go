// Package push_test exercises the Web Push HTTP handlers end-to-end through a
// real Echo instance (with RequireOwner wired) against an ephemeral PostgreSQL
// instance (testdb). Tests require Docker.
package push_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/push"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	pushsvc "github.com/technobecet/tsundoku/internal/push"
)

const (
	testSecret   = "push-handler-test-secret"
	testVAPIDKey = "BEl0test-public-key"
)

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	subs   *pushsvc.Service
	token  string
}

// newTestEnv stands up an Echo with the three push routes behind RequireOwner, a
// real subscription store over a fresh testdb, and a valid owner Bearer token.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	subs := pushsvc.NewService(client)
	h := handler.NewHandler(subs, testVAPIDKey)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/push/vapid-key", h.VAPIDKey)
	authed.POST("/push/subscriptions", h.Subscribe)
	authed.DELETE("/push/subscriptions", h.Unsubscribe)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, subs: subs, token: token}
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

// TestVAPIDKey_OK proves GET returns the server's public key.
func TestVAPIDKey_OK(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/push/vapid-key", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("VAPIDKey: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.VAPIDKeyDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Key != testVAPIDKey {
		t.Fatalf("key = %q, want %q", got.Key, testVAPIDKey)
	}
}

// TestSubscribe_Upserts proves a valid POST is a 204 and stores the subscription.
func TestSubscribe_Upserts(t *testing.T) {
	env := newTestEnv(t)
	body := `{"endpoint":"https://push.example/abc","keys":{"p256dh":"pk","auth":"ak"}}`
	rec := env.do(http.MethodPost, "/api/push/subscriptions", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("Subscribe: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	list, err := env.subs.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Endpoint != "https://push.example/abc" {
		t.Fatalf("subscription not stored: %+v", list)
	}
}

// TestSubscribe_EmptyAuth400 proves a missing auth key is rejected (fail-closed).
func TestSubscribe_EmptyAuth400(t *testing.T) {
	env := newTestEnv(t)
	body := `{"endpoint":"https://push.example/abc","keys":{"p256dh":"pk","auth":""}}`
	rec := env.do(http.MethodPost, "/api/push/subscriptions", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Subscribe empty auth: want 400, got %d", rec.Code)
	}
}

// TestSubscribe_BadEndpoint400 proves a non-http(s) endpoint is rejected.
func TestSubscribe_BadEndpoint400(t *testing.T) {
	env := newTestEnv(t)
	body := `{"endpoint":"ftp://nope","keys":{"p256dh":"pk","auth":"ak"}}`
	rec := env.do(http.MethodPost, "/api/push/subscriptions", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Subscribe bad endpoint: want 400, got %d", rec.Code)
	}
}

// TestUnsubscribe_Removes proves DELETE removes the subscription and returns 204.
func TestUnsubscribe_Removes(t *testing.T) {
	env := newTestEnv(t)
	if err := env.subs.Upsert(context.Background(), pushsvc.Subscription{
		Endpoint: "https://push.example/xyz", P256dh: "pk", Auth: "ak",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := `{"endpoint":"https://push.example/xyz"}`
	rec := env.do(http.MethodDelete, "/api/push/subscriptions", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("Unsubscribe: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	list, err := env.subs.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("subscription not removed: %+v", list)
	}
}

// TestPushRoutes_Unauthorized proves all three routes are behind RequireOwner.
func TestPushRoutes_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		method, target string
	}{
		{http.MethodGet, "/api/push/vapid-key"},
		{http.MethodPost, "/api/push/subscriptions"},
		{http.MethodDelete, "/api/push/subscriptions"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, nil)
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: want 401, got %d", tc.method, tc.target, rec.Code)
		}
	}
}
