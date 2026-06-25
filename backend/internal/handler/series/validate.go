// Package series contains the thin HTTP handlers for the library API: listing
// series, fetching one series' detail, recategorizing a series, and the
// per-category counts. Business logic lives in internal/series (the service);
// these handlers only bind/parse the request, validate it, call the service, and
// render the DTO. The service package internal/series collides with this
// package name, so it is imported aliased (seriessvc) in handler.go.
package series

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// defaultLimit is the page size applied when ?limit is omitted (or 0). It bounds
// an unparameterised list to a sensible page rather than the whole library.
const defaultLimit = 50

// maxLimit caps ?limit so a single request can never ask for an unbounded page.
const maxLimit = 200

// SetCategoryRequest is the PATCH /api/series/{id}/category request body.
type SetCategoryRequest struct {
	// Category is the target category enum value (Manga, Manhwa, Manhua, Comic, Other).
	Category string `json:"category"`
}

// validateCategoryFilter validates the optional ?category list filter. An empty
// string means "no filter" and yields (nil, nil). A non-empty value must be a
// legal Series.category enum value, else a 400 echo.HTTPError is returned. On
// success it returns a pointer to the validated value for series.ListFilter.
func validateCategoryFilter(raw string) (*string, error) {
	if raw == "" {
		return nil, nil
	}
	if err := entseries.CategoryValidator(entseries.Category(raw)); err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid category: "+raw)
	}
	return &raw, nil
}

// validatePagination parses and validates the optional ?limit and ?offset query
// params. Both must be non-negative integers; limit defaults to defaultLimit
// when absent/0 and is capped at maxLimit. A malformed or negative value yields
// a 400 echo.HTTPError.
func validatePagination(limitRaw, offsetRaw string) (limit, offset int, err error) {
	limit, err = parseNonNegative(limitRaw, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = parseNonNegative(offsetRaw, "offset")
	if err != nil {
		return 0, 0, err
	}

	if limit == 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
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

// validateID parses a UUID path param. subject names which id is being parsed
// ("series id", "provider id") so a malformed value yields a precise 400 body
// ("invalid <subject>") rather than always blaming the series id — this helper is
// reused for both the :id and :providerId params. The central middleware renders
// the message as {"message":...}.
func validateID(raw, subject string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid "+subject)
	}
	return id, nil
}

// validateSetCategory validates the PATCH body: category must be present and a
// legal enum value. A missing or illegal value yields a 400 echo.HTTPError.
// (The service re-validates the value defensively; validating here lets the
// handler reject obviously-bad input before touching the service.)
func validateSetCategory(req SetCategoryRequest) error {
	if req.Category == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "category is required")
	}
	if err := entseries.CategoryValidator(entseries.Category(req.Category)); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid category: "+req.Category)
	}
	return nil
}

// SetMonitoredRequest is the PATCH /api/series/{id}/monitored request body.
type SetMonitoredRequest struct {
	// Monitored indicates whether the series should be actively tracked for new chapters.
	Monitored *bool `json:"monitored"`
}

// validateSetMonitored validates the PATCH body: the monitored field must be
// explicitly present (a bool pointer so that omission is distinguishable from
// false). A missing field yields a 400 echo.HTTPError.
func validateSetMonitored(req SetMonitoredRequest) error {
	if req.Monitored == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "monitored is required")
	}
	return nil
}

// ProviderRankBody is one entry in the ReorderProvidersRequest.Providers list.
type ProviderRankBody struct {
	// ID is the SeriesProvider UUID to update.
	ID string `json:"id"`
	// Importance is the new priority/quality rank (non-negative; higher = preferred).
	Importance int `json:"importance"`
}

// ReorderProvidersRequest is the PATCH /api/series/{id}/providers request body.
type ReorderProvidersRequest struct {
	// Providers is the ordered list of (provider id, importance) pairs to apply.
	// At least one entry is required; importances are updated all-or-nothing.
	Providers []ProviderRankBody `json:"providers"`
}

// validateReorderProviders validates the PATCH body: at least one entry is required,
// each id must parse as a valid UUID, and each importance must be non-negative.
// Returns a []seriessvc.ProviderRank ready for the service, or a 400 echo.HTTPError.
func validateReorderProviders(req ReorderProvidersRequest) ([]seriessvc.ProviderRank, error) {
	if len(req.Providers) == 0 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "providers must have at least one entry")
	}
	ranks := make([]seriessvc.ProviderRank, len(req.Providers))
	for i, p := range req.Providers {
		id, err := uuid.Parse(p.ID)
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid provider id: "+p.ID)
		}
		if p.Importance < 0 {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "importance must be non-negative")
		}
		ranks[i] = seriessvc.ProviderRank{SeriesProviderID: id, Importance: p.Importance}
	}
	return ranks, nil
}
