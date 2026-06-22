// Package server_test provides black-box tests for the server package.
package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/server"
)

// TestHealthCheck verifies that HealthCheck responds with HTTP 200 and a body
// containing the string "ok", satisfying the /health contract.
func TestHealthCheck(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	if err := server.HealthCheck(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("body %q does not contain \"ok\"", rec.Body.String())
	}
}
