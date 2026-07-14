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

// Identify handles POST /api/series/:id/metadata/identify. It accepts EITHER
// the legacy single-pick {provider, remoteId} body or the multi-select
// {selections:[...]} body (see IdentifyRequest's doc comment).
//
// A single resolved selection (whichever shape it came from) routes through
// metadatasvc.Service.Identify — the owner's pick becomes the primary
// metadata_source and the engine auto-matches every OTHER registered
// provider by the primary's own title, merging the result ("anchor-then-
// aggregate", QCAT-228). TWO OR MORE selections route through
// metadatasvc.Service.IdentifyMerge instead — the owner's OWN picks are
// merged directly, with NO auto-matching beyond them (a deliberate,
// narrower multi-select merge — see IdentifyMerge's doc comment). Either
// path locks Series.metadata_locked (hand-curation guard against
// AutoIdentify). On success it returns the refreshed SeriesDetailDTO.
func (h *Handler) Identify(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	var req IdentifyRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	selections, err := validateIdentify(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if len(selections) == 1 {
		err = h.svc.Identify(ctx, id, selections[0].Provider, selections[0].RemoteID)
	} else {
		err = h.svc.IdentifyMerge(ctx, id, selections)
	}
	if err != nil {
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
// error middleware as a 500. ErrSeriesNotFound → 404; ErrProviderNotFound /
// ErrNoSelections → 400 (a caller-supplied bad request, not a missing
// resource); ErrAllSelectionsFailed is deliberately NOT special-cased — like
// Identify's own primary-fetch failure, "every provider I asked for failed"
// is a genuine upstream failure, not best-effort, so it falls through as a
// 500 (mirrors Identify's existing behavior for a failed primary fetch).
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, metadatasvc.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, metadatasvc.ErrProviderNotFound):
		return echo.NewHTTPError(http.StatusBadRequest, "unknown metadata provider")
	case errors.Is(err, metadatasvc.ErrNoSelections):
		return echo.NewHTTPError(http.StatusBadRequest, "at least one selection is required")
	default:
		return err
	}
}
