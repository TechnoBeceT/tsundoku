// Package library contains the thin HTTP handlers for the on-disk
// library-import workflow: scan storage, list staged imports, search a
// staged entry's title across Suwayomi sources, import a staged entry
// without re-downloading, and attach an additional source to an existing
// series. Business logic lives in internal/library (the service); these
// handlers only bind, validate, call the service, and render the DTO.
//
// The service package internal/library shares this package's name — see
// validate.go / this file for the unaliased "library" import (no conflict:
// a file's own package clause does not introduce an identifier); routes.go
// aliases the HANDLER import (libraryh) instead, since it also needs the
// service package unaliased.
package library

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/library"
)

// Handler holds the dependencies for the library-import HTTP handlers. All
// business logic lives in the library.Service; the handler is thin.
type Handler struct {
	svc *library.Service
}

// NewHandler constructs a Handler bound to a library.Service.
func NewHandler(svc *library.Service) *Handler {
	return &Handler{svc: svc}
}

// Scan handles POST /api/library/scan.
//
// It walks the storage root, upserts one ImportEntry per on-disk series
// (never downgrading an already-imported row), and returns the full staging
// list as []FoundSeriesDTO.
func (h *Handler) Scan(c echo.Context) error {
	out, err := h.svc.Scan(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// ListImports handles GET /api/library/imports?status=&limit=&offset=.
//
// The optional ?status filter must be one of pending/imported/skipped (empty
// means no filter). ?limit/?offset page the result (default 50, capped at
// 200 — see validatePagination). Returns the staged ImportEntry rows as
// []FoundSeriesDTO.
func (h *Handler) ListImports(c echo.Context) error {
	status, err := parseStatusFilter(c.QueryParam("status"))
	if err != nil {
		return err
	}
	limit, offset, err := validatePagination(c.QueryParam("limit"), c.QueryParam("offset"))
	if err != nil {
		return err
	}
	out, err := h.svc.ListImports(c.Request().Context(), status, limit, offset)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Match handles GET /api/library/imports/match?path=.
//
// path is a REQUIRED query param — never a URL path segment, since it is a
// filesystem path (encoding it as a segment would need extra escaping this
// API deliberately avoids). It searches every Suwayomi source for the staged
// entry's title and returns the grouped candidates as
// []imports.SearchGroupDTO, so the owner can pick one to pass as `match` on
// the subsequent Import call.
func (h *Handler) Match(c echo.Context) error {
	path, err := validatePath(c.QueryParam("path"))
	if err != nil {
		return err
	}
	out, err := h.svc.MatchCandidates(c.Request().Context(), path)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Import handles POST /api/library/import.
//
// The body carries {path, match?}: path (required) identifies a previously
// staged ImportEntry; match (optional) attaches an owner-chosen Suwayomi
// source at import time (source, mangaId, importance — all required together
// when match is present). Returns the imported series.SeriesDetailDTO (§16
// round-trip).
func (h *Handler) Import(c echo.Context) error {
	var body importBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := validateImportBody(body); err != nil {
		return err
	}

	var match *library.MatchInput
	if body.Match != nil {
		match = &library.MatchInput{
			Source:     body.Match.Source,
			MangaID:    body.Match.MangaID,
			Importance: body.Match.Importance,
		}
	}

	out, err := h.svc.Import(c.Request().Context(), body.Path, match)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Skip handles POST /api/library/imports/skip.
//
// The body carries {path}: path (required) identifies a previously staged
// ImportEntry (as returned by scan/list) that the owner wants to leave on
// disk without importing. Purely a status flip — no disk I/O, no row
// deletion (never-auto-delete invariant). Returns 204 on success.
func (h *Handler) Skip(c echo.Context) error {
	var body skipBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	path, err := validateSkipRequest(body)
	if err != nil {
		return err
	}

	if err := h.svc.Skip(c.Request().Context(), path); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AddProvider handles POST /api/series/:id/providers.
//
// It attaches an additional Suwayomi source {source, mangaId, importance} to
// an EXISTING series (upgrade-aware — see library.Service.AddProvider) and
// returns the refreshed series.SeriesDetailDTO.
func (h *Handler) AddProvider(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	var body addProviderBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := validateAddProviderBody(body); err != nil {
		return err
	}

	out, err := h.svc.AddProvider(c.Request().Context(), id, body.Source, body.MangaID, body.Importance)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// mapServiceError translates a library.Service sentinel error into the
// matching HTTP status, leaving any unexpected error to fall through to the
// central middleware as a 500. ErrSeriesNotFound / ErrEntryNotFound → 404;
// ErrProviderAlreadyPresent → 409; ErrSourceNotFound → 404 (a Suwayomi
// manga-fetch miss is treated the same as an unknown resource — see
// library.AddProvider, which wraps it via errors.Join so errors.Is still
// matches through the join).
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, library.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, library.ErrEntryNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "import entry not found")
	case errors.Is(err, library.ErrProviderAlreadyPresent):
		return echo.NewHTTPError(http.StatusConflict, "provider already attached to series")
	case errors.Is(err, library.ErrSourceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "source not found")
	default:
		return err
	}
}
