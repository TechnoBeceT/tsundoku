package middleware_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/middleware"
)

// newTestEcho builds a minimal Echo instance wired with our ErrorHandler and
// RequestID middleware, matching the production wiring order.
func newTestEcho() *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	e.Use(middleware.RequestID())
	return e
}

// TestErrorHandlerHTTPError confirms that a handler returning an echo.HTTPError
// renders the correct status code and JSON message envelope.
func TestErrorHandlerHTTPError(t *testing.T) {
	e := newTestEcho()
	e.GET("/test", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusBadRequest, "bad")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp middleware.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Message != "bad" {
		t.Errorf("message = %q, want %q", resp.Message, "bad")
	}
}

// TestErrorHandlerInternalError confirms that a handler returning a raw error
// (not an echo.HTTPError) renders 500 with a generic safe message and does NOT
// leak the internal error detail in the response body.
func TestErrorHandlerInternalError(t *testing.T) {
	const internalDetail = "secret internal detail"

	e := newTestEcho()
	e.GET("/test", func(c echo.Context) error {
		return errors.New(internalDetail)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if strings.Contains(body, internalDetail) {
		t.Errorf("response body leaks internal detail %q: %s", internalDetail, body)
	}

	var resp middleware.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, body)
	}
	if resp.Message == "" {
		t.Error("response message must not be empty")
	}
}

// TestErrorHandlerResponseShape confirms that the error envelope matches the
// OpenAPI ErrorResponse schema: a JSON object with exactly a "message" string.
func TestErrorHandlerResponseShape(t *testing.T) {
	e := newTestEcho()
	e.GET("/test", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := raw["message"]; !ok {
		t.Errorf("response JSON missing 'message' key: %v", raw)
	}
	// Ensure no extra keys leak (e.g. "error", "code") that would drift from OpenAPI.
	if len(raw) != 1 {
		t.Errorf("response JSON has unexpected keys: %v", raw)
	}
}

// TestRequestIDOnResponse confirms that every response carries an X-Request-Id
// header so clients can correlate log entries with their request.
func TestRequestIDOnResponse(t *testing.T) {
	e := newTestEcho()
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	rid := rec.Header().Get(middleware.RequestIDHeader)
	if rid == "" {
		t.Fatal("X-Request-Id header missing from response")
	}
}

// TestRequestIDPreservesIncoming confirms that a client-supplied X-Request-Id
// is echoed back unchanged, allowing distributed tracing.
func TestRequestIDPreservesIncoming(t *testing.T) {
	e := newTestEcho()
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	const clientID = "my-trace-id-12345"
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(middleware.RequestIDHeader, clientID)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	rid := rec.Header().Get(middleware.RequestIDHeader)
	if rid != clientID {
		t.Errorf("X-Request-Id = %q, want %q", rid, clientID)
	}
}

// TestRequestIDGeneratesWhenAbsent confirms that a new UUID is generated when
// no X-Request-Id header is supplied by the client.
func TestRequestIDGeneratesWhenAbsent(t *testing.T) {
	e := newTestEcho()
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	rid := rec.Header().Get(middleware.RequestIDHeader)
	if rid == "" {
		t.Fatal("X-Request-Id should be auto-generated when not supplied")
	}
}
