package series

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/category"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Handler holds the dependencies for the library (series) HTTP handlers.
// All business logic lives in the series.Service; the handler is thin.
type Handler struct {
	svc        *seriessvc.Service
	trigger    func()
	sw         suwayomi.Client
	viewSyncer ViewSyncer
}

// NewHandler constructs a Handler bound to a series.Service, an auto-converge
// trigger (called after a successful provider re-rank to re-evaluate upgrades
// immediately — M5; other routes do not use it), and a suwayomi.Client (used
// by the cover proxy endpoints to fetch cover images from Suwayomi).
func NewHandler(svc *seriessvc.Service, trigger func(), sw suwayomi.Client) *Handler {
	return &Handler{svc: svc, trigger: trigger, sw: sw}
}

// List handles GET /api/series.
//
// It parses the optional ?category filter (must be a legal enum value or empty)
// and the optional ?limit/?offset pagination (non-negative; limit defaults to
// 50, capped at 200), then returns a title-ASC page of SeriesSummaryDTO. An
// invalid category or pagination value yields 400.
func (h *Handler) List(c echo.Context) error {
	category, err := validateCategoryFilter(c.QueryParam("category"))
	if err != nil {
		return err
	}
	limit, offset, err := validatePagination(c.QueryParam("limit"), c.QueryParam("offset"))
	if err != nil {
		return err
	}

	out, err := h.svc.ListSeries(c.Request().Context(), seriessvc.ListFilter{
		Category: category,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return mapServiceError(err)
	}

	total, err := h.svc.CountSeries(c.Request().Context(), seriessvc.ListFilter{Category: category})
	if err != nil {
		return mapServiceError(err)
	}
	c.Response().Header().Set("X-Total-Count", strconv.Itoa(total))

	return c.JSON(http.StatusOK, out)
}

// Detail handles GET /api/series/:id.
//
// It parses the :id path param as a UUID (malformed → 400) and returns the
// full SeriesDetailDTO for that series. A missing series yields 404.
//
// Opening a series' detail page is a deliberate view action, so on a
// successful load it ALSO fires a detached, best-effort tracker-sync
// reconcile for the series (fireSyncOnView) IN ADDITION to the existing
// reading-triggered push — the response is built and returned from the
// already-fetched DTO regardless of the sync's outcome, so a slow or
// unreachable tracker can never delay or fail this request.
func (h *Handler) Detail(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	out, err := h.svc.GetSeries(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}
	h.fireSyncOnView(c.Request().Context(), id)
	return c.JSON(http.StatusOK, out)
}

// SetCategory handles PATCH /api/series/:id/category.
//
// It parses the :id path param and the {category} body, validates the category
// is a legal enum value (else 400), then recategorizes the series via the
// service (which moves the on-disk folder before updating the DB, so DB and disk
// never drift). On success it returns 200 with the UPDATED SeriesSummaryDTO so
// the caller sees the new state without a refetch (§16 full round-trip). A
// missing series yields 404; an invalid category yields 400.
func (h *Handler) SetCategory(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetCategoryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	categoryID, err := validateSetCategory(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetCategory(ctx, id, categoryID); err != nil {
		return mapServiceError(err)
	}

	// Return the updated summary so the response confirms the new category.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, detailToSummary(updated))
}

// SetMonitored handles PATCH /api/series/:id/monitored.
//
// It parses the :id path param and the {monitored: bool} body, then sets the
// series' monitored flag via the service. On success it returns 200 with the
// updated SeriesSummaryDTO so the caller sees the new state without a refetch
// (§16 full round-trip). A missing series yields 404; a missing/invalid body
// yields 400.
func (h *Handler) SetMonitored(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetMonitoredRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateSetMonitored(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetMonitored(ctx, id, *req.Monitored); err != nil {
		return mapServiceError(err)
	}

	// Return the updated summary so the response confirms the new monitored state.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, detailToSummary(updated))
}

// SetCompleted handles PATCH /api/series/:id/completed. It marks the series
// finished (or re-opens it) and returns the updated summary so the response
// confirms the new completed state.
func (h *Handler) SetCompleted(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetCompletedRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateSetCompleted(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetCompleted(ctx, id, *req.Completed); err != nil {
		return mapServiceError(err)
	}

	// Return the updated summary so the response confirms the new completed state.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, detailToSummary(updated))
}

// ReorderProviders handles PATCH /api/series/:id/providers.
//
// It parses the :id path param and the {providers: [{id, importance}]} body,
// validates each provider id is a valid UUID and the importance is non-negative,
// then updates provider importances all-or-nothing via the service. On success it
// returns 200 with the updated SeriesDetailDTO so importances are reflected in the
// response without a refetch (§16 full round-trip). A missing series yields 404;
// a provider that doesn't belong to the series yields 400; a bad UUID or empty
// list yields 400.
func (h *Handler) ReorderProviders(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req ReorderProvidersRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ranks, err := validateReorderProviders(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.ReorderProviders(ctx, id, ranks); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees the new importances without a refetch.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	// Auto-converge: re-rank may make a downloaded chapter's new top source a
	// strictly-better version — trigger a cycle so DetectUpgrades runs now (M5).
	h.trigger()
	return c.JSON(http.StatusOK, updated)
}

// RemoveProvider handles DELETE /api/series/:id/providers/:providerId. It
// removes one source from the series (deleting the provider row + its
// availability feed + sync state, clearing satisfied_by on affected chapters),
// keeping every downloaded CBZ, and returns the updated series detail. It does
// NOT trigger an auto-converge cycle — removal creates no wanted chapters (M6).
func (h *Handler) RemoveProvider(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	providerID, err := validateID(c.Param("providerId"), "provider id")
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.RemoveProvider(ctx, id, providerID); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees the removal without a refetch (§16).
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// SetIgnoreFractional handles
// PATCH /api/series/:id/providers/:providerId/ignore-fractional. It flags one
// source as a fractional re-uploader for this series (or clears the flag), so the
// source stops offering fractional-numbered chapters here. It DELETES NOTHING —
// existing feed rows and downloaded CBZs are kept, and un-ticking restores the
// source immediately.
//
// On success it returns 200 with the full SeriesDetailDTO so the Sources panel
// re-renders with the new flag AND the unchanged fractional evidence list,
// without a refetch (§16 round-trip).
func (h *Handler) SetIgnoreFractional(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	providerID, err := validateID(c.Param("providerId"), "provider id")
	if err != nil {
		return err
	}

	var req SetIgnoreFractionalRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateSetIgnoreFractional(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetIgnoreFractional(ctx, id, providerID, *req.IgnoreFractional); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees the new flag without a refetch (§16).
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// DeleteSeries handles DELETE /api/series/:id?deleteFiles=true|false. It hard-
// deletes the whole series (all DB rows); when deleteFiles=true it also removes
// the downloaded CBZs + library folder from disk. Returns 204 No Content.
func (h *Handler) DeleteSeries(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	deleteFiles, err := validateDeleteFiles(c.QueryParam("deleteFiles"))
	if err != nil {
		return err
	}
	if err := h.svc.DeleteSeries(c.Request().Context(), id, deleteFiles); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// DedupeFilesResult is the JSON response for POST /api/series/:id/dedupe-files:
// the number of superseded duplicate CBZ files removed from disk.
type DedupeFilesResult struct {
	// Removed is the count of duplicate CBZ files deleted by the sweep.
	Removed int `json:"removed"`
}

// DedupeFiles handles POST /api/series/:id/dedupe-files. It runs the owner-
// triggered duplicate-CBZ sweep over the series — removing every superseded CBZ
// that does not match a chapter's winning filename while keeping the winners —
// and returns {removed: N}. It performs no DB writes. A missing series yields 404.
func (h *Handler) DedupeFiles(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	removed, err := h.svc.DedupeFiles(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, DedupeFilesResult{Removed: removed})
}

// LibraryHealth handles GET /api/health — the library-wide source-health scan:
// every series with at least one stale or erroring source.
func (h *Handler) LibraryHealth(c echo.Context) error {
	res, err := h.svc.LibraryHealth(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, res)
}

// detailToSummary is the single detail→summary projection used by every mutating
// handler that returns an updated summary (SetCategory, SetMonitored, SetCompleted).
// Centralising the mapping here ensures that every field of SeriesSummaryDTO is
// always included — adding a new summary field only requires updating this one
// function; it cannot be silently omitted from a subset of call sites (§16).
func detailToSummary(d seriessvc.SeriesDetailDTO) seriessvc.SeriesSummaryDTO {
	return seriessvc.SeriesSummaryDTO{
		ID:                      d.ID,
		Title:                   d.Title,
		DisplayName:             d.DisplayName,
		Slug:                    d.Slug,
		Category:                d.Category,
		CoverURL:                d.CoverURL,
		Monitored:               d.Monitored,
		Completed:               d.Completed,
		NeedsSource:             d.NeedsSource,
		ChapterCounts:           d.ChapterCounts,
		CreatedAt:               d.CreatedAt,
		LastChapterDownloadedAt: d.LastChapterDownloadedAt,
	}
}

// SetMetadataSource handles PATCH /api/series/:id/metadata-source. It pins
// the series' metadata source to a given provider (or resets to automatic
// resolution when providerId is null/absent). On success it returns 200 with
// the full SeriesDetailDTO so the caller sees the new isMetadataSource flag
// without a refetch (§16).
func (h *Handler) SetMetadataSource(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetMetadataSourceRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	providerID, err := validateSetMetadataSource(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetMetadataSource(ctx, id, providerID); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees isMetadataSource without a refetch (§16).
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// mapServiceError translates a series.Service sentinel error into the matching
// HTTP status, leaving any unexpected error to fall through to the central
// middleware as a 500. ErrSeriesNotFound → 404; ErrProviderNotInSeries → 400;
// ErrNoCover → 404; category.ErrCategoryNotFound → 400 (an unknown categoryId in
// a recategorize body is a bad request, not a missing resource on this route);
// ErrChapterNotRemovable → 400 (the fractional-cleanup POST named a chapter that is
// not in the server-recomputed removable set — a bad selection, not a missing
// resource; the message names the offending chapter). ErrFractionalCleanupFailed →
// 500 with an HONEST message: the chapter rows were rolled back, but the CBZs deleted
// before the failing one are already gone, so the owner is told to re-run the cleanup
// (it is retry-safe) rather than being handed a bare "internal server error".
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, seriessvc.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, seriessvc.ErrChapterNotRemovable):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, seriessvc.ErrFractionalCleanupFailed):
		// The rows were rolled back, but the CBZs deleted before the failure are
		// already gone — say so, and say that a retry finishes the job.
		return echo.NewHTTPError(http.StatusInternalServerError,
			"fractional cleanup failed while deleting files: no chapters were removed, but some CBZ files may already be deleted — re-run the cleanup to finish")
	case errors.Is(err, category.ErrCategoryNotFound):
		return echo.NewHTTPError(http.StatusBadRequest, "unknown category")
	case errors.Is(err, seriessvc.ErrProviderNotInSeries):
		return echo.NewHTTPError(http.StatusBadRequest, "provider does not belong to series")
	case errors.Is(err, seriessvc.ErrNoCover):
		return echo.NewHTTPError(http.StatusNotFound, "no cover available")
	case errors.Is(err, seriessvc.ErrCoverFetchFailed):
		return echo.NewHTTPError(http.StatusBadGateway, "cover fetch failed")
	default:
		return err
	}
}
