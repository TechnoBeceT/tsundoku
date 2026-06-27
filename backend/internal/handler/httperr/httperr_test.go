package httperr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
)

// TestBadRequest verifies BadRequest returns a 400 echo.HTTPError carrying the
// message verbatim (the message the error middleware renders to the client).
func TestBadRequest(t *testing.T) {
	err := httperr.BadRequest("pkgName required")

	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("BadRequest: want *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("BadRequest code: got %d, want %d", he.Code, http.StatusBadRequest)
	}
	if he.Message != "pkgName required" {
		t.Errorf("BadRequest message: got %v, want %q", he.Message, "pkgName required")
	}
}

// TestUpstream verifies Upstream maps an upstream failure to a 502 echo.HTTPError
// whose message is prefixed with "suwayomi: " and carries the wrapped error text.
func TestUpstream(t *testing.T) {
	err := httperr.Upstream(errors.New("connection refused"))

	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("Upstream: want *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusBadGateway {
		t.Errorf("Upstream code: got %d, want %d", he.Code, http.StatusBadGateway)
	}
	if he.Message != "suwayomi: connection refused" {
		t.Errorf("Upstream message: got %v, want %q", he.Message, "suwayomi: connection refused")
	}
}
