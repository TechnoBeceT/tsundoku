package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

func newTestService() *auth.Service {
	return auth.NewService("middleware-test-secret")
}

func TestRequireOwner_NoBearer(t *testing.T) {
	e := echo.New()
	svc := newTestService()
	mw := middleware.RequireOwner(svc)

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
	mw := middleware.RequireOwner(svc)

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
	mw := middleware.RequireOwner(svc)

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
