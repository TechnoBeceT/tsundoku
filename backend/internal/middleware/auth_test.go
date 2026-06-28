package middleware_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/pkg/authcookie"
)

func newTestService() *auth.Service {
	return auth.NewService("middleware-test-secret")
}

// pastHalfLifeToken constructs a signed token whose half-life is already in the
// past, so that auth.Service.ShouldRenew will return true. It reproduces the
// exact token format used by the auth package (header.payload.signature, each
// base64url-encoded) so that auth.Service.Validate can verify it. iat is set to
// 20 days ago and exp to 10 days from now: half-life = iat + 15 days = 5 days
// ago → past.
func pastHalfLifeToken(t *testing.T, secret string, ownerID uuid.UUID) string {
	t.Helper()
	iat := time.Now().Add(-20 * 24 * time.Hour).Unix()
	exp := time.Now().Add(10 * 24 * time.Hour).Unix()

	header := base64.RawURLEncoding.EncodeToString(mustJSON(t, map[string]string{"alg": "HS256"}))
	payloadBytes := mustJSON(t, struct {
		Sub string `json:"sub"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}{Sub: ownerID.String(), Iat: iat, Exp: exp})
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadBytes)

	unsigned := header + "." + payloadPart
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return unsigned + "." + sig
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustJSON: %v", err)
	}
	return b
}

func TestRequireOwner_NoBearer(t *testing.T) {
	e := echo.New()
	svc := newTestService()
	mw := middleware.RequireOwner(svc, false)

	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	if err == nil {
		t.Fatal("RequireOwner: expected error when no Authorization header, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("RequireOwner: expected *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusUnauthorized {
		t.Errorf("RequireOwner: expected 401, got %d", he.Code)
	}
}

func TestRequireOwner_ValidBearer(t *testing.T) {
	e := echo.New()
	svc := newTestService()
	mw := middleware.RequireOwner(svc, false)

	ownerID := uuid.New()
	tok, err := svc.Issue(ownerID)
	if err != nil {
		t.Fatalf("Issue: unexpected error: %v", err)
	}

	var capturedID any
	handler := mw(func(c echo.Context) error {
		capturedID = c.Get(middleware.OwnerIDKey)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handler(c); err != nil {
		t.Fatalf("RequireOwner: unexpected error for valid token: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("RequireOwner: expected 200, got %d", rec.Code)
	}
	gotID, ok := capturedID.(uuid.UUID)
	if !ok {
		t.Fatalf("RequireOwner: owner_id context value has wrong type %T", capturedID)
	}
	if gotID != ownerID {
		t.Errorf("RequireOwner: got owner_id %v, want %v", gotID, ownerID)
	}
}

func TestRequireOwner_TamperedBearer(t *testing.T) {
	e := echo.New()
	svc := newTestService()
	mw := middleware.RequireOwner(svc, false)

	tok, err := svc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue: unexpected error: %v", err)
	}
	// Tamper with the token by flipping the last byte.
	tampered := []byte(tok)
	tampered[len(tampered)-1] ^= 0x01

	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+string(tampered))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = handler(c)
	if err == nil {
		t.Fatal("RequireOwner: expected error for tampered token, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("RequireOwner: expected *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusUnauthorized {
		t.Errorf("RequireOwner: expected 401, got %d", he.Code)
	}
}

// TestRequireOwner_CookieAuth verifies that the session cookie is accepted as a
// valid authentication source, without any Authorization header.
func TestRequireOwner_CookieAuth(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	svc := auth.NewService(secret)
	id := uuid.New()
	tok, err := svc.Issue(id)
	if err != nil {
		t.Fatalf("Issue: unexpected error: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: authcookie.Name, Value: tok}) //nolint:gosec // test-only request cookie
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := middleware.RequireOwner(svc, true)(func(c echo.Context) error { return c.NoContent(200) })
	if err := h(c); err != nil {
		t.Fatalf("cookie auth should pass: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("RequireOwner cookie auth: expected 200, got %d", rec.Code)
	}
}

// TestRequireOwner_BearerStillWorks verifies that the Authorization: Bearer
// header still works after the cookie-first change (no cookie set).
func TestRequireOwner_BearerStillWorks(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	svc := auth.NewService(secret)
	id := uuid.New()
	tok, err := svc.Issue(id)
	if err != nil {
		t.Fatalf("Issue: unexpected error: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	// Deliberately no cookie.
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := middleware.RequireOwner(svc, false)(func(c echo.Context) error { return c.NoContent(200) })
	if err := h(c); err != nil {
		t.Fatalf("Bearer fallback should pass: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("RequireOwner Bearer fallback: expected 200, got %d", rec.Code)
	}
}

// TestRequireOwner_SlidingRenewalSetsCookie verifies that a token already past
// its half-life causes a fresh Set-Cookie response header. The token is
// constructed manually (iat=20d ago, exp=10d from now) so ShouldRenew returns
// true without relying on internal auth helpers.
func TestRequireOwner_SlidingRenewalSetsCookie(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	svc := auth.NewService(secret)
	id := uuid.New()

	tok := pastHalfLifeToken(t, secret, id)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: authcookie.Name, Value: tok}) //nolint:gosec // test-only request cookie
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := middleware.RequireOwner(svc, false)(func(c echo.Context) error { return c.NoContent(200) })
	if err := h(c); err != nil {
		t.Fatalf("past-half-life token should still pass: %v", err)
	}

	// Check that a fresh Set-Cookie header was written.
	cookies := rec.Result().Cookies()
	var found bool
	for _, ck := range cookies {
		if ck.Name == authcookie.Name {
			found = true
			if ck.Value == tok {
				t.Errorf("sliding renewal: expected a fresh (different) token, got the same one")
			}
			break
		}
	}
	if !found {
		t.Errorf("sliding renewal: expected a Set-Cookie for %q, got none; headers: %v",
			authcookie.Name, rec.Header())
	}
}
