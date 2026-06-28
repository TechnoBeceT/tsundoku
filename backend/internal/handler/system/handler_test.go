package system_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/handler/system"
)

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
