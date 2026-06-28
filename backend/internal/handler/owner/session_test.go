package owner_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/authcookie"
)

// TestLogin_SetsSessionCookie verifies that a successful login response
// carries a tsundoku_session Set-Cookie header that is HttpOnly and has a
// positive MaxAge, while still including the token in the JSON body.
func TestLogin_SetsSessionCookie(t *testing.T) {
	h, e, _ := newHandlerWithClient(t)

	// First claim so a valid user exists.
	claimCode := callClaim(t, e, h, "admin", "password123")
	if claimCode != http.StatusOK {
		t.Fatalf("setup claim: expected 200, got %d", claimCode)
	}

	rec := callLoginFull(t, e, h, "admin", "password123")
	if rec.Code != http.StatusOK {
		t.Fatalf("Login: expected 200, got %d", rec.Code)
	}

	// JSON body must still contain the token.
	var resp owner.TokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Login: decode body: %v", err)
	}
	if resp.Token == "" {
		t.Error("Login: expected non-empty token in body")
	}

	// Set-Cookie must include the session cookie with correct attributes.
	sessionCookie := findCookie(rec, authcookie.Name)
	if sessionCookie == nil {
		t.Fatalf("Login: %q Set-Cookie not found in response", authcookie.Name)
	}
	if !sessionCookie.HttpOnly {
		t.Error("Login: session cookie must be HttpOnly")
	}
	if sessionCookie.MaxAge <= 0 {
		t.Errorf("Login: session cookie MaxAge must be > 0, got %d", sessionCookie.MaxAge)
	}
}

// TestLogout_ClearsCookie verifies that Logout returns 204 and sets a
// tsundoku_session cookie with MaxAge < 0 (immediate expiry).
func TestLogout_ClearsCookie(t *testing.T) {
	// Logout only uses the cookieSecure field — client and auth can be nil.
	h := owner.NewHandler(nil, nil, true)
	e := echo.New()

	req := httptest.NewRequest(http.MethodPost, "/api/owner/logout", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.Logout(c); err != nil {
		t.Fatalf("Logout: unexpected error: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("Logout: expected 204, got %d", rec.Code)
	}

	sessionCookie := findCookie(rec, authcookie.Name)
	if sessionCookie == nil {
		t.Fatalf("Logout: %q Set-Cookie not found in response", authcookie.Name)
	}
	if sessionCookie.MaxAge >= 0 {
		t.Errorf("Logout: session cookie MaxAge must be < 0, got %d", sessionCookie.MaxAge)
	}
}

// TestMe_ReturnsOwnerID verifies that Me echoes back the authenticated
// owner UUID stored on the Echo context under middleware.OwnerIDKey.
func TestMe_ReturnsOwnerID(t *testing.T) {
	h := owner.NewHandler(nil, nil, false)
	e := echo.New()

	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/owner/me", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(mw.OwnerIDKey, id)

	if err := h.Me(c); err != nil {
		t.Fatalf("Me: unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Me: expected 200, got %d", rec.Code)
	}

	var resp owner.MeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Me: decode body: %v", err)
	}
	if resp.OwnerID != id.String() {
		t.Errorf("Me: ownerId = %q, want %q", resp.OwnerID, id.String())
	}
}

// TestMe_MissingID verifies that Me returns 401 when no owner UUID is on the context.
func TestMe_MissingID(t *testing.T) {
	h := owner.NewHandler(nil, nil, false)
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/api/owner/me", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	// Intentionally do NOT set OwnerIDKey.

	err := h.Me(c)
	if err == nil {
		t.Fatal("Me: expected an error, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("Me: expected *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusUnauthorized {
		t.Errorf("Me: expected 401, got %d", he.Code)
	}
}

// callLoginFull performs a login POST and returns the full ResponseRecorder
// so callers can inspect both body and headers/cookies.
func callLoginFull(t *testing.T, e *echo.Echo, h *owner.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/owner/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.Login(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			rec.WriteHeader(he.Code)
		}
	}
	return rec
}

// findCookie returns the first cookie with the given name from the recorder,
// or nil if not found.
func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}
