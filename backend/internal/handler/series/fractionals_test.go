package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// TestLibraryFractionals_OK: GET /api/library/fractionals lists the series with a
// downloaded fractional, carrying both counts and the whole-series toggle state.
// The seedCleanup fixture (kaliscan ignored, comix live) has 5.1 (removable) and
// 6.1 (protected — comix carries it), so fractionalCount=2, removableCount=1.
func TestLibraryFractionals_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, _ := seedCleanup(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/library/fractionals", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.LibraryFractionalsDTO
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
	if row.FractionalCount != 2 || row.RemovableCount != 1 {
		t.Errorf("counts = frac %d / removable %d, want 2 / 1", row.FractionalCount, row.RemovableCount)
	}
	if row.ProvidersTotal != 2 || row.ProvidersIgnoring != 1 || row.AllProvidersIgnoring {
		t.Errorf("toggle state = total %d ignoring %d all %v, want 2/1/false",
			row.ProvidersTotal, row.ProvidersIgnoring, row.AllProvidersIgnoring)
	}
}

// TestLibraryFractionals_EmptyIsArrayNotNull: an empty library answers 200 with
// series: [] (never null), so the FE never has to guard a null.
func TestLibraryFractionals_EmptyIsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodGet, "/api/library/fractionals", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"series":[]`) {
		t.Errorf("body = %s, want series marshalled as [] (never null)", rec.Body.String())
	}
}

// TestSetIgnoreFractionalForSeries_OK: PATCH flags every source, returns the full
// SeriesDetailDTO with all sources' ignoreFractional=true (§16 round-trip), and
// persists the change.
func TestSetIgnoreFractionalForSeries_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, _ := seedCleanup(ctx, t, env)

	rec := env.do(http.MethodPatch, "/api/series/"+id.String()+"/ignore-fractional", `{"ignoreFractional":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Providers) == 0 {
		t.Fatal("no providers in the response detail")
	}
	for _, p := range got.Providers {
		if !p.IgnoreFractional {
			t.Errorf("source %q ignoreFractional=false in the response, want true (whole-series toggle)", p.Provider)
		}
	}
	// Round-trip: every SeriesProvider row is flagged in the DB.
	for _, p := range env.client.SeriesProvider.Query().AllX(ctx) {
		if !p.IgnoreFractional {
			t.Errorf("DB source %q ignoreFractional=false, want true", p.Provider)
		}
	}
}

// TestSetIgnoreFractionalForSeries_NotFound: an unknown series is a 404.
func TestSetIgnoreFractionalForSeries_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/ignore-fractional", `{"ignoreFractional":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetIgnoreFractionalForSeries_BadBody: a missing ignoreFractional field or a
// malformed series id is a 400 (a suppression switch must never silently default).
func TestSetIgnoreFractionalForSeries_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, _ := seedCleanup(ctx, t, env)

	cases := map[string]struct{ target, body string }{
		"missing field": {"/api/series/" + id.String() + "/ignore-fractional", `{}`},
		"non-bool":      {"/api/series/" + id.String() + "/ignore-fractional", `{"ignoreFractional":"yes"}`},
		"bad series id": {"/api/series/not-a-uuid/ignore-fractional", `{"ignoreFractional":true}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := env.do(http.MethodPatch, tc.target, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
			}
		})
	}
}
