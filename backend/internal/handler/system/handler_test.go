package system_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/handler/system"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

// TestGetSystem_Unauthorized proves GET /api/system is behind RequireOwner.
// No Docker/testdb needed — the 401 fires in middleware before any handler logic.
func TestGetSystem_Unauthorized(t *testing.T) {
	const testSecret = "system-handler-test-secret"
	authSvc := auth.NewService(testSecret)
	cfg := &config.Config{}
	h := system.NewHandler(cfg)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	grp := e.Group("/api", middleware.RequireOwner(authSvc, false))
	grp.GET("/system", h.Get)

	// Issue a valid token just to confirm the route exists; we send NO token below.
	if _, err := authSvc.Issue(uuid.New()); err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/system without token: want 401, got %d", rec.Code)
	}
}

func TestGetSystem_ReturnsConfigValues(t *testing.T) {
	cfg := &config.Config{}
	cfg.Storage.Folder = "/library"
	cfg.Server.Port = "9833"
	cfg.Database.Host = "db"
	cfg.Database.Port = "5432"
	cfg.Database.Name = "tsundoku"
	cfg.Database.User = "secretuser"
	cfg.Database.Password = "secretpw"

	e := echo.New()
	h := system.NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	rec := httptest.NewRecorder()
	if err := h.Get(e.NewContext(req, rec)); err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("Get: want 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "/library") {
		t.Errorf("body missing storage folder: %s", body)
	}
	if !strings.Contains(body, "9833") {
		t.Errorf("body missing server port: %s", body)
	}
	if !strings.Contains(body, "db:5432/tsundoku") {
		t.Errorf("body missing database DSN: %s", body)
	}
	// SECURITY: credentials must never leak
	if strings.Contains(body, "secretpw") {
		t.Errorf("body must not contain database password: %s", body)
	}
	if strings.Contains(body, "secretuser") {
		t.Errorf("body must not contain database user: %s", body)
	}
}
