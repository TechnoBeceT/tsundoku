package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// TestLibrarySourceless_OK: GET /api/library/sourceless lists the series with a
// downloaded sourceless chapter. The seedSourcelessCleanup fixture (comix
// carries "5" only) has one sourceless chapter ("7"), so sourcelessCount=1.
func TestLibrarySourceless_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, _ := seedSourcelessCleanup(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/library/sourceless", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.LibrarySourcelessDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Series) != 1 {
		t.Fatalf("want 1 series, got %d (%+v)", len(got.Series), got.Series)
	}
	row := got.Series[0]
	if row.SeriesID != id.String() {
		t.Errorf("seriesId = %s, want %s", row.SeriesID, id)
	}
	if row.SourcelessCount != 1 {
		t.Errorf("sourcelessCount = %d, want 1", row.SourcelessCount)
	}
}

// TestLibrarySourceless_EmptyIsArrayNotNull: an empty library answers 200 with
// series: [] (never null), so the FE never has to guard a null.
func TestLibrarySourceless_EmptyIsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodGet, "/api/library/sourceless", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"series":[]`) {
		t.Errorf("body = %s, want series marshalled as [] (never null)", rec.Body.String())
	}
}
