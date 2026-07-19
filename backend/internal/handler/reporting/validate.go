// Package reporting holds the thin HTTP handlers for the Source Health Console's
// read/reporting API: the period overview, the per-source rollup, the raw event
// feed, and the success/fail timeline. Every aggregation is done in the DB by the
// service (internal/reporting); these handlers only parse + validate the request,
// call the service, and render the DTO (bind → validate → service → DTO). The
// service package internal/reporting collides with this package name, so it is
// imported aliased (reportingsvc) in handler.go.
package reporting

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/handler/pagination"
	reportingsvc "github.com/technobecet/tsundoku/internal/reporting"
)

// parsePeriod validates the optional ?period against the closed set (24h/7d/30d).
// An empty value defaults to 24h (the narrowest, cheapest window); any other
// value is a 400 naming the allowed set.
func parsePeriod(raw string) (reportingsvc.Period, error) {
	switch reportingsvc.Period(strings.TrimSpace(raw)) {
	case "":
		return reportingsvc.Period24h, nil
	case reportingsvc.Period24h:
		return reportingsvc.Period24h, nil
	case reportingsvc.Period7d:
		return reportingsvc.Period7d, nil
	case reportingsvc.Period30d:
		return reportingsvc.Period30d, nil
	default:
		return "", echo.NewHTTPError(http.StatusBadRequest, "period must be one of 24h, 7d, 30d")
	}
}

// parseSort validates the optional ?sort against the closed set. An empty value
// defaults to "failures" (the most-broken-first default the console leads with);
// any other value is a 400.
func parseSort(raw string) (reportingsvc.Sort, error) {
	switch reportingsvc.Sort(strings.TrimSpace(raw)) {
	case "":
		return reportingsvc.SortFailures, nil
	case reportingsvc.SortFailures:
		return reportingsvc.SortFailures, nil
	case reportingsvc.SortLatency:
		return reportingsvc.SortLatency, nil
	case reportingsvc.SortEvents:
		return reportingsvc.SortEvents, nil
	default:
		return "", echo.NewHTTPError(http.StatusBadRequest, "sort must be one of failures, latency, events")
	}
}

// parseBucket validates the optional ?bucket against the closed set (hour/day).
// An empty value defaults to "hour"; any other value is a 400.
func parseBucket(raw string) (reportingsvc.Bucket, error) {
	switch reportingsvc.Bucket(strings.TrimSpace(raw)) {
	case "":
		return reportingsvc.BucketHour, nil
	case reportingsvc.BucketHour:
		return reportingsvc.BucketHour, nil
	case reportingsvc.BucketDay:
		return reportingsvc.BucketDay, nil
	default:
		return "", echo.NewHTTPError(http.StatusBadRequest, "bucket must be one of hour, day")
	}
}

// parseStatus validates the optional ?status filter against the SourceEvent
// status enum. An empty value yields (nil, nil) — "any status". An unknown value
// is a 400 naming it.
func parseStatus(raw string) (*entsourceevent.Status, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	st := entsourceevent.Status(v)
	if err := entsourceevent.StatusValidator(st); err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "unknown status: "+v)
	}
	return &st, nil
}

// parseEventType validates the optional ?eventType filter against the SourceEvent
// event_type enum. An empty value yields (nil, nil) — "any type". An unknown
// value is a 400 naming it.
func parseEventType(raw string) (*entsourceevent.EventType, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	et := entsourceevent.EventType(v)
	if err := entsourceevent.EventTypeValidator(et); err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "unknown eventType: "+v)
	}
	return &et, nil
}

// parseSourceKey validates the required :sourceKey path param. It must be
// non-blank; the AllSourcesKey sentinel ("__all__") is accepted verbatim (the
// service treats it as the global feed). A blank value is a 400.
func parseSourceKey(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "sourceKey is required")
	}
	return v, nil
}

// validatePagination parses the optional ?limit / ?offset via the shared
// pagination validator (§2 DRY — the same rules as series/downloads/library).
func validatePagination(limitRaw, offsetRaw string) (limit, offset int, err error) {
	return pagination.Validate(limitRaw, offsetRaw)
}
