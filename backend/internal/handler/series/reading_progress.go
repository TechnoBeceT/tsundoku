package series

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// TrackerProgressSetter is the narrow port SetReadingProgress uses to
// force-set every one of the series' bound trackers to the owner's chosen
// chapter — satisfied by syncsvc.Service.SetSeriesProgress. Depending on
// this narrow interface (rather than importing internal/tracker/syncsvc
// directly) mirrors ViewSyncer's own layering rationale (see that
// interface's doc comment): an ent-touching orchestration service is
// consumed structurally, never imported by name.
type TrackerProgressSetter interface {
	// SetSeriesProgress force-sets every one of seriesID's TrackBinding
	// entries to target, bypassing the never-regress push rule — see
	// syncsvc.Service.SetSeriesProgress's own doc comment.
	SetSeriesProgress(ctx context.Context, seriesID uuid.UUID, target float64) error
}

// WithTrackerProgressSetter attaches the tracker force-set hook and returns
// the handler, mirroring WithViewSyncer's fluent wiring (production attaches
// the SAME syncsvc.Service instance both hooks share — see routes.go).
// UNLIKE ViewSyncer (an optional best-effort reconcile), production always
// wires this: SetReadingProgress's whole point (QCAT-242) is propagating to
// every bound tracker. A Handler with none attached is only the shape every
// OTHER handler/series test already uses (they never exercise
// SetReadingProgress); calling SetReadingProgress against one is a no-op on
// the tracker half, never a panic.
func (h *Handler) WithTrackerProgressSetter(p TrackerProgressSetter) *Handler {
	h.trackerProgress = p
	return h
}

// SetReadingProgress handles POST /api/series/:id/reading-progress
// (QCAT-242, owner-ratified 2026-07-14): the owner's "re-read from start" or
// "jump to chapter N" action. It resets the series' LOCAL chapter read-state
// to "<= chapter read, > chapter unread" (series.Service.SetReadingProgress)
// and then force-sets EVERY one of the series' bound trackers to the same
// target (TrackerProgressSetter.SetSeriesProgress) — the never-regress push
// rule is deliberately bypassed there; this is the one sanctioned path that
// may lower a tracker's progress, because an explicit owner reset is a
// different intent than an accidental stale push.
//
// On success it returns 200 with the full, refreshed SeriesDetailDTO so the
// chapter list and the Trackers section both reflect the reset without a
// refetch (§16 round-trip). A missing series yields 404 (from the chapter
// reset's existence check); a malformed body yields 400. A genuine tracker
// upstream failure surfaces as a 4xx carrying the tracker's own message
// (mapTrackerProgressError) rather than a silent drop or a bare 500 — the
// owner explicitly asked for this reset, so §16 requires the real failure to
// be visible, the same posture UpdateTrack already established
// (handler/trackers.UpdateTrack's doc comment).
func (h *Handler) SetReadingProgress(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetReadingProgressRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	target, err := validateSetReadingProgress(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if _, err := h.svc.SetReadingProgress(ctx, id, target); err != nil {
		return mapServiceError(err)
	}

	if h.trackerProgress != nil {
		if err := h.trackerProgress.SetSeriesProgress(ctx, id, target); err != nil {
			return mapTrackerProgressError(err)
		}
	}

	// Return the updated detail so the caller sees both the reset chapters
	// and the reset tracker bindings without a refetch (§16).
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// mapTrackerProgressError translates a SetSeriesProgress failure into the
// documented HTTP status. A genuine per-binding upstream failure
// (tracker.UpstreamError, wrapped by syncsvc.SetSeriesProgress; note the
// method aggregates per-binding failures via errors.Join, and errors.As
// walks a Join tree, so this still matches when only SOME bindings failed)
// surfaces as a 400 carrying the tracker's own rejection text — mirroring
// handler/trackers.mapServiceError's identical UpstreamError→400 mapping, so
// the owner sees the real message through Cloudflare instead of a bare 502.
// Any other error (a DB failure persisting a binding) is returned UNWRAPPED
// so the central middleware renders the safe, opaque 500.
func mapTrackerProgressError(err error) error {
	var upErr *tracker.UpstreamError
	if errors.As(err, &upErr) {
		return httperr.BadRequest(err.Error())
	}
	return err
}
