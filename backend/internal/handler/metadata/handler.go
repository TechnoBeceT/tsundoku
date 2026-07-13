// Package metadata contains the thin HTTP handlers for the Phase-1 native
// metadata engine (see spec/metadata-engine-phase1 §5): cross-provider
// search, per-series identify (owner's "anchor-then-aggregate" pick),
// cover-candidate gallery, and cover pick. Business logic lives in
// internal/metadatasvc; these handlers only bind/parse the request, validate
// it, call the service, and render the DTO. After a mutating call (Identify /
// SetCover) the refreshed series.SeriesDetailDTO is returned so the owner
// sees the change without a refetch (§16 round-trip — mirrors
// handler/series's SetMetadataSource).
//
// The internal/metadata package (the pure provider models — SearchResult,
// CoverCandidate, ...) collides with this package's own name "metadata", so
// it is imported aliased as metadatamodel throughout (mirrors handler/series
// aliasing internal/series as seriessvc, handler/suwayomi aliasing
// internal/suwayomi as suwayomicli — the established collision pattern in
// this codebase).
package metadata

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/handler/sourcefilter"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// Handler holds the dependencies for the metadata-engine HTTP handlers: the
// metadata orchestration service and the series read service (used to render
// the refreshed SeriesDetailDTO after Identify/SetCover).
type Handler struct {
	svc       *metadatasvc.Service
	seriesSvc *seriessvc.Service
}

// NewHandler constructs a Handler bound to a metadatasvc.Service and the
// shared series.Service (the same instance the series handler uses, so a
// series-detail round-trip reflects every other domain's writes too).
func NewHandler(svc *metadatasvc.Service, seriesSvc *seriessvc.Service) *Handler {
	return &Handler{svc: svc, seriesSvc: seriesSvc}
}

// Search handles GET /api/metadata/search?q=&providers=. It fans a free-text
// query out across every registered metadata provider (or the ?providers CSV
// subset, reusing the shared sourcefilter.Parse — the same "?sources CSV"
// rule the imports/library discovery endpoints use) and returns the raw
// candidate gallery. Nothing is persisted — this feeds the Identify modal's
// picker; POST /api/series/:id/metadata/identify does the actual write.
func (h *Handler) Search(c echo.Context) error {
	q, err := validateQuery(c.QueryParam("q"))
	if err != nil {
		return err
	}
	providerKeys := sourcefilter.Parse(c.QueryParam("providers"))

	results, err := h.svc.Search(c.Request().Context(), q, providerKeys)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, toSearchResultDTOs(results))
}

// Identify handles POST /api/series/:id/metadata/identify. The owner's chosen
// (provider, remoteId) pair becomes the primary metadata_source; the engine
// auto-matches every other provider by the primary's own title and merges the
// result (metadatasvc.Service.Identify — the "anchor-then-aggregate" pick,
// QCAT-228). On success it returns the refreshed SeriesDetailDTO.
func (h *Handler) Identify(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	var req IdentifyRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	provider, remoteID, err := validateIdentify(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.Identify(ctx, id, provider, remoteID); err != nil {
		return mapServiceError(err)
	}
	updated, err := h.seriesSvc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// Covers handles GET /api/series/:id/metadata/covers — the aggregated cover
// gallery (every metadata provider's cover for the series' own title) behind
// the owner's cover-picker modal. Nothing is persisted; see SetCover for the
// pick itself.
func (h *Handler) Covers(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	candidates, err := h.svc.CoverCandidates(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, toCoverCandidateDTOs(candidates))
}

// SetCover handles POST /api/series/:id/cover — the owner's explicit cover
// pick (from either the metadata-provider gallery or a library source's own
// cover). It fetches coverUrl's bytes, caches them via the Local Cover Cache,
// and records cover_source independently of metadata_source (QCAT-228: a
// cover pick never implies a metadata re-merge). On success it returns the
// refreshed SeriesDetailDTO (fresh coverUrl/coverSource/cover_version — §16).
func (h *Handler) SetCover(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	var req SetCoverRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	if err := validateSetCover(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetCover(ctx, id, req.SourceKind, req.SourceRef, req.CoverURL); err != nil {
		return mapServiceError(err)
	}
	updated, err := h.seriesSvc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// mapServiceError translates a metadatasvc sentinel error into the matching
// HTTP status, leaving any unexpected error (a DB failure, an upstream
// provider fetch failure inside Identify/SetCover — metadatasvc does not
// distinguish the two with its own sentinel) to fall through to the central
// error middleware as a 500. ErrSeriesNotFound → 404; ErrProviderNotFound →
// 400 (the caller supplied a provider key the registry does not hold, a bad
// request, not a missing resource).
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, metadatasvc.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, metadatasvc.ErrProviderNotFound):
		return echo.NewHTTPError(http.StatusBadRequest, "unknown metadata provider")
	default:
		return err
	}
}
