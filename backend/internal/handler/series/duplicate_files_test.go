package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// TestLibraryDuplicateFiles_OK: GET /api/library/duplicate-files lists the series
// whose folder carries a removable duplicate CBZ. seedAlphaSagaDupes writes
// Alpha Saga's chapter-1 winner plus one leftover copy, so fileCount=1 and the
// reclaimable byte total is that file's size.
func TestLibraryDuplicateFiles_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	seedAlphaSagaDupes(t, env)

	rec := env.do(http.MethodGet, "/api/library/duplicate-files", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.LibraryDuplicateFilesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Series) != 1 {
		t.Fatalf("want 1 series, got %d (%+v)", len(got.Series), got.Series)
	}
	row := got.Series[0]
	if row.SeriesID != env.mangaID.String() {
		t.Errorf("seriesId = %s, want %s", row.SeriesID, env.mangaID)
	}
	if row.FileCount != 1 {
		t.Errorf("fileCount = %d, want 1", row.FileCount)
	}
	if row.ReclaimableBytes <= 0 {
		t.Errorf("reclaimableBytes = %d, want the leftover file's real size", row.ReclaimableBytes)
	}
	if got.TotalFiles != 1 || got.TotalBytes != row.ReclaimableBytes {
		t.Errorf("totals = %d/%d, want 1/%d", got.TotalFiles, got.TotalBytes, row.ReclaimableBytes)
	}
}

// TestLibraryDuplicateFiles_EmptyIsArrayNotNull: a library with nothing removable
// answers 200 with series: [] (never null), so the FE never has to guard a null.
func TestLibraryDuplicateFiles_EmptyIsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodGet, "/api/library/duplicate-files", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"series":[]`) {
		t.Errorf("body = %s, want series marshalled as [] (never null)", rec.Body.String())
	}
}
