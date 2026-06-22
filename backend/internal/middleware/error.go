// Package middleware — see auth.go for package-level documentation.
package middleware

import (
	"errors"
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
)

// ErrorResponse is the JSON envelope returned for all error responses.
// It matches the OpenAPI 3.1 ErrorResponse schema in api/openapi.yaml so that
// clients have a single, stable shape to decode regardless of error type.
type ErrorResponse struct {
	// Message is a safe, human-readable description of the error.
	// Internal error detail is never included — it is logged server-side only.
	Message string `json:"message"`
}

// ErrorHandler is an Echo HTTPErrorHandler that renders any returned error as a
// JSON ErrorResponse with the appropriate HTTP status code.
//
// Rules:
//   - echo.HTTPError: use the code and message from the error; if the message
//     is not a string, fall back to the standard HTTP status text.
//   - Any other error: log the internal detail, return 500 with a safe generic
//     message so that raw error text is never leaked to the client.
//
// This function should be assigned to echo.Echo.HTTPErrorHandler after
// construction so that it intercepts all errors returned from handlers and
// middleware, including those surfaced by echo's Recover middleware.
func ErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		// Headers already flushed (e.g. SSE stream that errored mid-stream);
		// nothing more we can do without corrupting the response.
		return
	}

	code := http.StatusInternalServerError
	msg := "internal server error"

	var he *echo.HTTPError
	if ok := isHTTPError(err, &he); ok {
		code = he.Code
		if s, ok := he.Message.(string); ok && s != "" {
			msg = s
		} else {
			msg = http.StatusText(code)
		}
		// Log internal detail when the HTTPError wraps a cause.
		if he.Internal != nil {
			log.Printf("request error: %v (internal: %v)", he.Message, he.Internal)
		}
	} else {
		// Unknown error — log internally, return generic message to caller.
		// Never expose raw error text: it may contain stack traces, DSNs, or
		// other sensitive internal details.
		log.Printf("unhandled error: %v", err)
	}

	_ = c.JSON(code, ErrorResponse{Message: msg})
}

// isHTTPError checks whether err is (or wraps) an *echo.HTTPError and, if so,
// writes the unwrapped value into target. errors.As is used so that wrapped
// HTTPErrors (e.g. fmt.Errorf("…: %w", httpErr)) are handled correctly, not
// just direct type assertions.
func isHTTPError(err error, target **echo.HTTPError) bool {
	return errors.As(err, target)
}
