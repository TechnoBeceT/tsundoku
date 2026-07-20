// Package downloads contains the thin HTTP handlers for the cross-library
// download-activity API: the state-filtered chapter list and the owner retry
// actions. Business logic lives in internal/downloads (the service); these
// handlers only parse + validate the request, call the service, and render the
// DTO. The service package internal/downloads collides with this package name,
// so it is imported aliased (downloadssvc) in handler.go.
package downloads

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	downloadssvc "github.com/technobecet/tsundoku/internal/downloads"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/handler/pagination"
)

// parseStates parses the REQUIRED ?state CSV into a set of chapter states. An
// empty value yields a 400 (the state filter is mandatory — listing every
// chapter regardless of state is not an offered view). An unknown value yields a
// 400 naming the offending token. Whitespace around tokens is tolerated.
func parseStates(raw string) ([]entchapter.State, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "state is required")
	}
	return parseStateCSV(raw)
}

// parseRetryStates parses the OPTIONAL retry-all ?state CSV. An empty value
// yields (nil, nil) so the service applies its default scope (failed +
// permanently_failed). Every supplied state must be both a valid enum value and
// retryable, else a 400 (you cannot "retry" a downloading or wanted chapter).
func parseRetryStates(raw string) ([]entchapter.State, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	states, err := parseStateCSV(raw)
	if err != nil {
		return nil, err
	}
	for _, st := range states {
		if !downloadssvc.IsRetryableState(st) {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "state is not retryable: "+st.String())
		}
	}
	return states, nil
}

// parseStateCSV splits a comma-separated state list, trims each token, and
// validates it against the Chapter.state enum. Empty tokens (from stray commas /
// whitespace) are skipped; a token that is not a legal state yields a 400 naming
// it. A list with no valid tokens yields a 400.
func parseStateCSV(raw string) ([]entchapter.State, error) {
	parts := strings.Split(raw, ",")
	states := make([]entchapter.State, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		st := entchapter.State(v)
		if err := entchapter.StateValidator(st); err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "unknown state: "+v)
		}
		states = append(states, st)
	}
	if len(states) == 0 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "state is required")
	}
	return states, nil
}

// validatePagination parses the optional ?limit and ?offset query params.
// Delegates to the shared internal/handler/pagination package (§2 DRY — this
// logic was byte-identical across series/downloads/library until extracted).
func validatePagination(limitRaw, offsetRaw string) (limit, offset int, err error) {
	return pagination.Validate(limitRaw, offsetRaw)
}

// validateID parses a required UUID path param. subject names which id is being
// parsed so a malformed value yields a precise 400 ("invalid <subject>").
func validateID(raw, subject string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid "+subject)
	}
	return id, nil
}

// parseOptionalID parses an OPTIONAL UUID query param. An empty value yields
// (nil, nil) — the scope is simply unset. A malformed value yields a 400.
func parseOptionalID(raw, subject string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid "+subject)
	}
	return &id, nil
}

// parseOptionalBool parses an OPTIONAL boolean query param. An empty value yields
// (false, nil) — the flag is simply off. Only "true"/"false" are accepted (case-
// insensitive); anything else yields a 400 naming the param.
func parseOptionalBool(raw, subject string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, echo.NewHTTPError(http.StatusBadRequest, "invalid "+subject)
	}
}
