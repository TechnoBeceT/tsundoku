// Package category contains the thin HTTP handlers for the category API:
// listing categories with counts, creating, renaming/reordering, and deleting a
// category. Business logic lives in internal/category (the service); these
// handlers only bind/parse the request, validate it, call the service, and
// render the DTO. The service package internal/category collides with this
// package name, so it is imported aliased (categorysvc) in handler.go.
package category

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// CreateCategoryRequest is the POST /api/categories request body. SortOrder is
// optional (a pointer so omission is distinguishable from an explicit 0).
type CreateCategoryRequest struct {
	// Name is the new category's display label AND on-disk folder name.
	Name string `json:"name"`
	// SortOrder is the optional display order (defaults to 0 when omitted).
	SortOrder *int `json:"sortOrder"`
}

// UpdateCategoryRequest is the PATCH /api/categories/{id} request body. Both
// fields are optional pointers so a request may rename, reorder, or do both;
// at least one must be present.
type UpdateCategoryRequest struct {
	// Name, when set, renames the category (moves its on-disk folder).
	Name *string `json:"name"`
	// SortOrder, when set, updates the display order (DB-only).
	SortOrder *int `json:"sortOrder"`
}

// validateID parses a category UUID path param, yielding a 400 on a malformed
// value. The central middleware renders the message as {"message":...}.
func validateID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid category id")
	}
	return id, nil
}

// validateUpdate confirms an UpdateCategoryRequest carries at least one field to
// change. An empty body (neither name nor sortOrder) is a 400 — there is nothing
// to do.
func validateUpdate(req UpdateCategoryRequest) error {
	if req.Name == nil && req.SortOrder == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "at least one of name or sortOrder is required")
	}
	return nil
}
