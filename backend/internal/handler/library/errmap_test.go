// White-box (package library) test for mapServiceError's HTTP-status mapping.
// It lives in package library — not library_test — so it can call the unexported
// mapServiceError directly and assert the status each service sentinel maps to,
// without standing up a full HTTP round-trip for each one.
package library

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/library"
)

// TestMapServiceError_SourceStatuses pins the honest source-error taxonomy: a
// true membership miss is 404, a cooled-down source is 503, and a genuine
// engine-host fetch failure is 502 (never the old phantom 404).
func TestMapServiceError_SourceStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"true membership miss → 404", library.ErrSourceNotFound, http.StatusNotFound},
		{"cooled-down → 503", fmt.Errorf("%w (source 12345)", library.ErrSourceUnavailable), http.StatusServiceUnavailable},
		{"upstream fetch failure → 502", fmt.Errorf("%w: page fetch failed", library.ErrSourceUpstream), http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var he *echo.HTTPError
			if !errors.As(mapServiceError(tc.err), &he) {
				t.Fatalf("mapped error is not *echo.HTTPError for %v", tc.err)
			}
			if he.Code != tc.want {
				t.Errorf("status = %d, want %d", he.Code, tc.want)
			}
		})
	}
}

// TestMapServiceError_MergeInFlight pins the single-flight status: a per-series
// dedup that found the series' merge latch already held is a 409, carrying the
// sentinel's own caller-safe text. It must never fall through to a 500, and it
// must never be answered as a 200 with zero counts — the caller has to be able
// to tell "retry in a moment" apart from "there was nothing to merge" (GAP-120).
func TestMapServiceError_MergeInFlight(t *testing.T) {
	var he *echo.HTTPError
	if !errors.As(mapServiceError(library.ErrMergeInFlight), &he) {
		t.Fatal("ErrMergeInFlight did not map to an *echo.HTTPError")
	}
	if he.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", he.Code, http.StatusConflict)
	}
	if he.Message != library.ErrMergeInFlight.Error() {
		t.Errorf("message = %v, want the sentinel's own text", he.Message)
	}
}
