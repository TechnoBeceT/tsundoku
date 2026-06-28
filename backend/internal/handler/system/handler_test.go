package system_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, h.Get(e.NewContext(req, rec)))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "/library")
	require.Contains(t, body, "9833")
	require.Contains(t, body, "db:5432/tsundoku")
	// SECURITY: credentials must never leak
	require.NotContains(t, body, "secretpw")
	require.NotContains(t, body, "secretuser")
}
