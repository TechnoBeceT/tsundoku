package pagination_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/pagination"
)

// TestValidate_DefaultsWhenLimitZero verifies an absent (empty-string) limit
// defaults to pagination.DefaultLimit, and offset defaults to 0.
func TestValidate_DefaultsWhenLimitZero(t *testing.T) {
	limit, offset, err := pagination.Validate("", "")
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if limit != pagination.DefaultLimit {
		t.Errorf("limit: got %d, want %d", limit, pagination.DefaultLimit)
	}
	if offset != 0 {
		t.Errorf("offset: got %d, want 0", offset)
	}
}

// TestValidate_ExplicitZeroLimitDefaults verifies an explicit "0" limit is
// treated the same as an absent one (defaults, does not pass through as 0).
func TestValidate_ExplicitZeroLimitDefaults(t *testing.T) {
	limit, _, err := pagination.Validate("0", "0")
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if limit != pagination.DefaultLimit {
		t.Errorf("limit: got %d, want %d", limit, pagination.DefaultLimit)
	}
}

// TestValidate_CapsAtMaxLimit verifies a limit above pagination.MaxLimit is
// silently capped rather than rejected.
func TestValidate_CapsAtMaxLimit(t *testing.T) {
	limit, _, err := pagination.Validate("500", "")
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if limit != pagination.MaxLimit {
		t.Errorf("limit: got %d, want %d", limit, pagination.MaxLimit)
	}
}

// TestValidate_MidValuePassesThrough verifies a limit strictly between 0 and
// MaxLimit passes through unchanged.
func TestValidate_MidValuePassesThrough(t *testing.T) {
	limit, offset, err := pagination.Validate("25", "10")
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if limit != 25 {
		t.Errorf("limit: got %d, want 25", limit)
	}
	if offset != 10 {
		t.Errorf("offset: got %d, want 10", offset)
	}
}

// TestValidate_OffsetPassthrough verifies a non-zero offset is never defaulted
// or capped (only limit has a default/cap).
func TestValidate_OffsetPassthrough(t *testing.T) {
	_, offset, err := pagination.Validate("", "999")
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if offset != 999 {
		t.Errorf("offset: got %d, want 999", offset)
	}
}

// TestValidate_NegativeLimitRejected verifies a negative limit yields a 400
// echo.HTTPError naming "limit".
func TestValidate_NegativeLimitRejected(t *testing.T) {
	_, _, err := pagination.Validate("-1", "")
	assertBadRequest(t, err, "limit must be a non-negative integer")
}

// TestValidate_NonNumericLimitRejected verifies a malformed (non-integer)
// limit yields a 400 naming "limit".
func TestValidate_NonNumericLimitRejected(t *testing.T) {
	_, _, err := pagination.Validate("abc", "")
	assertBadRequest(t, err, "limit must be a non-negative integer")
}

// TestValidate_NegativeOffsetRejected verifies a negative offset yields a 400
// echo.HTTPError naming "offset" — and that limit is checked first (offset
// error only surfaces once limit itself is valid).
func TestValidate_NegativeOffsetRejected(t *testing.T) {
	_, _, err := pagination.Validate("", "-5")
	assertBadRequest(t, err, "offset must be a non-negative integer")
}

// TestValidate_NonNumericOffsetRejected verifies a malformed (non-integer)
// offset yields a 400 naming "offset".
func TestValidate_NonNumericOffsetRejected(t *testing.T) {
	_, _, err := pagination.Validate("", "xyz")
	assertBadRequest(t, err, "offset must be a non-negative integer")
}

// assertBadRequest fails the test unless err is a 400 echo.HTTPError with the
// exact message wantMsg — the "naming token" contract the handler tests rely on.
func assertBadRequest(t *testing.T, err error, wantMsg string) {
	t.Helper()
	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("want *echo.HTTPError, got %T (%v)", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("code: got %d, want %d", he.Code, http.StatusBadRequest)
	}
	if he.Message != wantMsg {
		t.Errorf("message: got %v, want %q", he.Message, wantMsg)
	}
}
