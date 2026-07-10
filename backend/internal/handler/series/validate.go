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
	"github.com/technobecet/tsundoku/internal/handler/pagination"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// SetCategoryRequest is the PATCH /api/series/{id}/category request body.
type SetCategoryRequest struct {
	// CategoryID is the target Category UUID to file the series under.
	CategoryID string `json:"categoryId"`
}

// validateCategoryFilter validates the optional ?category filter. An empty
// string means "no filter" and yields (nil, nil). A non-empty value is the
// category NAME to filter by (categories are now user-defined, so there is no
// fixed enum to validate against — an unknown name simply matches no series).
func validateCategoryFilter(raw string) (*string, error) {
	if raw == "" {
		return nil, nil
	}
	return &raw, nil
}

// validatePagination parses and validates the optional ?limit and ?offset query
// params. Delegates to the shared internal/handler/pagination package (§2 DRY —
// this logic was byte-identical across series/downloads/library until extracted).
func validatePagination(limitRaw, offsetRaw string) (limit, offset int, err error) {
	return pagination.Validate(limitRaw, offsetRaw)
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

// validateSetCategory validates the PATCH body: categoryId must be present and a
// valid UUID. A missing or malformed value yields a 400 echo.HTTPError. (Whether
// the category actually exists is checked by the service, which maps an unknown
// category to a 400 via mapServiceError.)
func validateSetCategory(req SetCategoryRequest) (uuid.UUID, error) {
	if req.CategoryID == "" {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "categoryId is required")
	}
	id, err := uuid.Parse(req.CategoryID)
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid categoryId: "+req.CategoryID)
	}
	return id, nil
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

// SetCompletedRequest is the PATCH /api/series/{id}/completed request body.
type SetCompletedRequest struct {
	// Completed indicates whether the series is finished (no more chapters expected).
	Completed *bool `json:"completed"`
}

// validateSetCompleted validates the PATCH body: the completed field must be
// explicitly present (a bool pointer so omission is distinguishable from false).
// A missing field yields a 400 echo.HTTPError.
func validateSetCompleted(req SetCompletedRequest) error {
	if req.Completed == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "completed is required")
	}
	return nil
}

// ProviderRankBody is one entry in the ReorderProvidersRequest.Providers list.
type ProviderRankBody struct {
	// ID is the SeriesProvider UUID to update.
	ID string `json:"id"`
	// Importance expresses the desired priority ORDER (higher = preferred). Any
	// integer is accepted — the service normalizes the submitted importances to a
	// clean non-negative descending spread, so even a legacy negative value is
	// tolerated and self-healed rather than rejected.
	Importance int `json:"importance"`
}

// ReorderProvidersRequest is the PATCH /api/series/{id}/providers request body.
type ReorderProvidersRequest struct {
	// Providers is the ordered list of (provider id, importance) pairs to apply.
	// At least one entry is required; importances are updated all-or-nothing.
	Providers []ProviderRankBody `json:"providers"`
}

// validateDeleteFiles parses the required deleteFiles query param for the series
// DELETE. It must be explicitly "true" or "false" (no default) so an
// irreversible delete always carries the owner's explicit intent. An empty or
// non-boolean value yields a 400 echo.HTTPError.
func validateDeleteFiles(raw string) (bool, error) {
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "":
		return false, echo.NewHTTPError(http.StatusBadRequest, "deleteFiles is required")
	default:
		return false, echo.NewHTTPError(http.StatusBadRequest, "deleteFiles must be true or false")
	}
}

// validatePageIndex parses the :n page-index path param for the reader page-bytes
// endpoint. It must be a non-negative integer; a non-integer or negative value
// yields a 400. An in-range-but-too-large index is NOT a validation error — the
// service maps it to a 404 via disk.ErrPageOutOfRange, because how many pages a
// chapter has is data, not request shape.
func validatePageIndex(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "invalid page index")
	}
	return n, nil
}

// SetMetadataSourceRequest is the PATCH /api/series/{id}/metadata-source request body.
// ProviderID nil or "" means "auto" (reset to the highest-importance provider).
type SetMetadataSourceRequest struct {
	// ProviderID is the SeriesProvider UUID to pin as the metadata source,
	// or null/absent to reset to automatic resolution.
	ProviderID *string `json:"providerId"`
}

// validateSetMetadataSource parses the PATCH body for SetMetadataSource. A nil
// or empty providerId resets to auto-resolution (returns nil pointer). A non-empty
// providerId must parse as a valid UUID — a malformed value yields a 400. Returns
// the UUID pointer ready for the service.
func validateSetMetadataSource(req SetMetadataSourceRequest) (*uuid.UUID, error) {
	if req.ProviderID == nil || *req.ProviderID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*req.ProviderID)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid provider id: "+*req.ProviderID)
	}
	return &id, nil
}

// validateReorderProviders validates the PATCH body: at least one entry is
// required and each id must parse as a valid UUID. The importance value is NOT
// range-checked here — the service normalizes the submitted importances to a
// clean non-negative descending spread (only their relative order matters), so a
// negative importance (legacy bad data) is tolerated and self-healed rather than
// rejected with a 400. Returns a []seriessvc.ProviderRank ready for the service,
// or a 400 echo.HTTPError.
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
		ranks[i] = seriessvc.ProviderRank{SeriesProviderID: id, Importance: p.Importance}
	}
	return ranks, nil
}
