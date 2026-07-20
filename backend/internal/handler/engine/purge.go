package engine

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/sourcepurge"
)

// WithPurge attaches the source-purge service the /api/engine/purge-* endpoints
// need. Returns the receiver for chaining off NewHandler; the topology-status,
// apk and sources endpoints do not use it, so it stays nil for the constructor's
// other call sites (mirrors WithSourceStatus).
func (h *Handler) WithPurge(purge *sourcepurge.Service) *Handler {
	h.purge = purge
	return h
}

// PurgeSource handles POST /api/engine/purge-source. Body: {sourceId, sourceName}.
// It runs the full cascade — removing every SeriesProvider on the source (via the
// sanctioned RemoveProvider cascade), its metric + circuit-breaker rows, and
// honestly un-pinning any chapter left sourceless — while KEEPING every CBZ and
// Chapter row (never-auto-delete). Returns a summary of exactly what was removed.
// A malformed body is a 400; a purge failure falls through to the central
// middleware as a 500 (the owner sees the error state — §16).
func (h *Handler) PurgeSource(c echo.Context) error {
	var req PurgeSourceRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	id, name, err := validateSourceIdentity(req.SourceID, req.SourceName)
	if err != nil {
		return err
	}
	summary, err := h.purge.PurgeSource(c.Request().Context(), id, name)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSourceSummaryDTO(summary))
}

// PreviewSource handles GET /api/engine/purge-source/preview?sourceId=&sourceName=.
// It counts what PurgeSource WOULD remove without mutating anything (the confirm
// dialog's blast-radius figures). sourceName is optional here (it is resolved from
// the metric/provider rows when absent).
func (h *Handler) PreviewSource(c echo.Context) error {
	id := strings.TrimSpace(c.QueryParam("sourceId"))
	if id == "" {
		return httperr.BadRequest("sourceId is required")
	}
	preview, err := h.purge.PreviewSource(c.Request().Context(), id, strings.TrimSpace(c.QueryParam("sourceName")))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSourcePreviewDTO(preview))
}

// PurgeExtension handles POST /api/engine/purge-extension. Body: {pkgName}. It
// resolves the extension's sources from the durable HarvestedExtension store and
// purges each (fault-isolated per source). A malformed body is a 400; a failure to
// read the durable store falls through to the central middleware as a 500.
func (h *Handler) PurgeExtension(c echo.Context) error {
	var req PurgeExtensionRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	pkgName := strings.TrimSpace(req.PkgName)
	if pkgName == "" {
		return httperr.BadRequest("pkgName is required")
	}
	summary, err := h.purge.PurgeExtension(c.Request().Context(), pkgName)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toExtensionSummaryDTO(summary))
}

// PreviewExtension handles GET /api/engine/purge-extension/preview?pkgName=. It
// aggregates the dry-run counts across every source the extension provides.
func (h *Handler) PreviewExtension(c echo.Context) error {
	pkgName := strings.TrimSpace(c.QueryParam("pkgName"))
	if pkgName == "" {
		return httperr.BadRequest("pkgName is required")
	}
	preview, err := h.purge.PreviewExtension(c.Request().Context(), pkgName)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toExtensionPreviewDTO(preview))
}

// validateSourceIdentity trims and confirms both halves of a physical-source
// identity are present. For an explicit purge both are required: the numeric id
// keys the SourceMetric row + the live SeriesProviders, while the NAME keys the
// breaker row + the disk-reconciled SeriesProviders — a purge needs both to be
// complete.
func validateSourceIdentity(sourceID, sourceName string) (id, name string, err error) {
	id = strings.TrimSpace(sourceID)
	name = strings.TrimSpace(sourceName)
	if id == "" {
		return "", "", httperr.BadRequest("sourceId is required")
	}
	if name == "" {
		return "", "", httperr.BadRequest("sourceName is required")
	}
	return id, name, nil
}
