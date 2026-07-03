// Package pagination holds the shared ?limit/?offset validation reused by every
// handler package that paginates a list (series, downloads, library). All three
// previously carried a byte-identical validatePagination + parseNonNegative
// implementation; this package is the single source of truth (§2 DRY) so a
// future change to the paging rules (default, cap, or error wording) only has
// to happen once.
package pagination

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

// DefaultLimit is the page size applied when ?limit is omitted (or 0). It
// bounds an unparameterised list to a sensible page rather than the whole
// table.
const DefaultLimit = 50

// MaxLimit caps ?limit so a single request can never ask for an unbounded
// page.
const MaxLimit = 200

// Validate parses and validates the optional ?limit and ?offset query params.
// Both must be non-negative integers; limit defaults to DefaultLimit when
// absent/0 and is capped at MaxLimit. A malformed or negative value yields a
// 400 echo.HTTPError naming the offending parameter ("limit" or "offset").
func Validate(limitRaw, offsetRaw string) (limit, offset int, err error) {
	limit, err = parseNonNegative(limitRaw, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = parseNonNegative(offsetRaw, "offset")
	if err != nil {
		return 0, 0, err
	}

	if limit == 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return limit, offset, nil
}

// parseNonNegative parses raw as a non-negative integer, returning 0 for an
// empty string (the param is absent). A malformed or negative value yields a
// 400 echo.HTTPError naming the offending parameter.
func parseNonNegative(raw, name string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, name+" must be a non-negative integer")
	}
	return v, nil
}
