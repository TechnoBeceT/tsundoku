package reporting

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	reportingsvc "github.com/technobecet/tsundoku/internal/reporting"
)

// Handler serves the Source Health Console reporting endpoints:
//
//	GET /api/reporting/overview
//	GET /api/reporting/sources
//	GET /api/reporting/source/:sourceKey/events
//	GET /api/reporting/source/:sourceKey/timeline
//
// It is a thin layer over reportingsvc.Service — parse + validate the request,
// call the service (which does all aggregation in SQL), render the DTO. All four
// are read-only owner endpoints (behind mw.RequireOwner).
type Handler struct {
	svc *reportingsvc.Service
}

// NewHandler constructs a reporting Handler over the aggregation service.
func NewHandler(svc *reportingsvc.Service) *Handler {
	return &Handler{svc: svc}
}

// Overview handles GET /api/reporting/overview?period=. It returns the period
// dashboard (KPIs + leaderboards + recent errors). A bad period is a 400; a DB
// error falls through to the central middleware as a 500.
func (h *Handler) Overview(c echo.Context) error {
	period, err := parsePeriod(c.QueryParam("period"))
	if err != nil {
		return err
	}
	report, err := h.svc.Overview(c.Request().Context(), period, time.Now())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toOverviewDTO(report))
}

// Sources handles GET /api/reporting/sources?period=&sort=. It returns the
// per-source rollup, sorted per sort. A bad period/sort is a 400; a DB error
// falls through as a 500.
func (h *Handler) Sources(c echo.Context) error {
	period, err := parsePeriod(c.QueryParam("period"))
	if err != nil {
		return err
	}
	order, err := parseSort(c.QueryParam("sort"))
	if err != nil {
		return err
	}
	reports, err := h.svc.Sources(c.Request().Context(), period, order, time.Now())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSourceReportDTOs(reports))
}

// Events handles GET /api/reporting/source/:sourceKey/events?status=&eventType=
// &limit=&offset=. It returns one filtered, paginated page of the raw event feed
// (total + items). The :sourceKey "__all__" sentinel selects the global feed. A
// bad sourceKey / status / eventType / pagination value is a 400; a DB error
// falls through as a 500.
func (h *Handler) Events(c echo.Context) error {
	sourceKey, err := parseSourceKey(c.Param("sourceKey"))
	if err != nil {
		return err
	}
	status, err := parseStatus(c.QueryParam("status"))
	if err != nil {
		return err
	}
	eventType, err := parseEventType(c.QueryParam("eventType"))
	if err != nil {
		return err
	}
	limit, offset, err := validatePagination(c.QueryParam("limit"), c.QueryParam("offset"))
	if err != nil {
		return err
	}

	feed, err := h.svc.Events(c.Request().Context(), reportingsvc.EventFilter{
		SourceKey: sourceKey,
		Status:    status,
		EventType: eventType,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toEventListDTO(feed))
}

// Timeline handles GET /api/reporting/source/:sourceKey/timeline?bucket=&period=.
// It returns the success/fail counts bucketed by hour or day (the histogram
// data). The :sourceKey "__all__" sentinel selects the global timeline. A bad
// sourceKey / bucket / period is a 400; a DB error falls through as a 500.
func (h *Handler) Timeline(c echo.Context) error {
	sourceKey, err := parseSourceKey(c.Param("sourceKey"))
	if err != nil {
		return err
	}
	bucket, err := parseBucket(c.QueryParam("bucket"))
	if err != nil {
		return err
	}
	period, err := parsePeriod(c.QueryParam("period"))
	if err != nil {
		return err
	}

	buckets, err := h.svc.Timeline(c.Request().Context(), sourceKey, bucket, period, time.Now())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toTimelineDTO(buckets))
}
