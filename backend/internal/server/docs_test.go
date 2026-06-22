package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/server"
)

// TestDocsUI verifies that GET /docs returns HTTP 200 and includes the spec
// title so we know Scalar is wired to the correct spec.
func TestDocsUI(t *testing.T) {
	e := echo.New()
	server.RegisterDocs(e)

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /docs: got %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Tsundoku API") {
		t.Fatalf("GET /docs: body does not contain spec title; got:\n%s", body)
	}
	if !strings.Contains(body, "/docs/openapi.yaml") {
		t.Fatalf("GET /docs: body does not reference /docs/openapi.yaml; got:\n%s", body)
	}
}

// TestDocsSpec verifies that GET /docs/openapi.yaml returns HTTP 200, a YAML
// content-type, and a body that contains the spec title from openapi.yaml.
func TestDocsSpec(t *testing.T) {
	e := echo.New()
	server.RegisterDocs(e)

	req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /docs/openapi.yaml: got %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "yaml") {
		t.Fatalf("GET /docs/openapi.yaml: Content-Type %q does not contain \"yaml\"", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Tsundoku API") {
		t.Fatalf("GET /docs/openapi.yaml: body does not contain spec title; got:\n%s", body)
	}
}
