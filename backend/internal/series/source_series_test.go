package series_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

// provSpec describes one SeriesProvider to seed: its raw provider identity value
// (a numeric source id for a live row, a display name for a disk-origin row), its
// display name (empty for a disk row), and its importance.
type provSpec struct {
	provider     string
	providerName string
	importance   int
}

// seedSeriesWithProviders creates one series (in the Manga category) carrying the
// given providers, and returns its id.
func seedSeriesWithProviders(ctx context.Context, t *testing.T, client *ent.Client, title string, provs ...provSpec) uuid.UUID {
	t.Helper()
	s := client.Series.Create().
		SetTitle(title).
		SetSlug("slug-" + title).
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)
	for _, p := range provs {
		client.SeriesProvider.Create().
			SetSeriesID(s.ID).
			SetProvider(p.provider).
			SetProviderName(p.providerName).
			SetImportance(p.importance).
			SaveX(ctx)
	}
	return s.ID
}

// bySeriesTitle indexes a result slice by title so assertions can target a row
// regardless of ordering.
func bySeriesTitle(rows []series.SourceSeriesDTO) map[string]series.SourceSeriesDTO {
	out := make(map[string]series.SourceSeriesDTO, len(rows))
	for _, r := range rows {
		out[r.Title] = r
	}
	return out
}

// assertSourceSeries asserts the row keyed by title has the expected goes-dark /
// alternative-count / take-over shape. Extracted so each test stays a flat list
// of expectations rather than a stack of compound conditionals.
func assertSourceSeries(t *testing.T, byTitle map[string]series.SourceSeriesDTO, title string, goesDark bool, count int, top string) {
	t.Helper()
	r, ok := byTitle[title]
	if !ok {
		t.Fatalf("%q missing from result: %+v", title, byTitle)
	}
	if r.GoesDark != goesDark || r.AlternativeCount != count || r.TopAlternative != top {
		t.Errorf("%q = {goesDark:%v count:%d top:%q}, want {goesDark:%v count:%d top:%q}",
			title, r.GoesDark, r.AlternativeCount, r.TopAlternative, goesDark, count, top)
	}
}

// TestSeriesForSource_ClassifiesGoesDarkAndAlternative proves the core split: a
// series whose only provider IS the source goes dark (no alternative), while a
// series with other providers keeps them — reporting the count and the
// highest-importance take-over source. Source 7 = "Comix" is stored the LIVE way
// (numeric provider "7", display name "Comix").
func TestSeriesForSource_ClassifiesGoesDarkAndAlternative(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedSeriesWithProviders(ctx, t, client, "Only Comix",
		provSpec{provider: "7", providerName: "Comix", importance: 10})
	seedSeriesWithProviders(ctx, t, client, "Comix Plus Two",
		provSpec{provider: "7", providerName: "Comix", importance: 10},
		provSpec{provider: "9", providerName: "Flame", importance: 30},
		provSpec{provider: "4", providerName: "Asura", importance: 20})
	// A series that does NOT carry the source must be absent from the result.
	seedSeriesWithProviders(ctx, t, client, "No Comix",
		provSpec{provider: "9", providerName: "Flame", importance: 10})

	svc := series.NewService(client, t.TempDir(), 14)
	rows, err := svc.SeriesForSource(ctx, 7, "Comix")
	if err != nil {
		t.Fatalf("SeriesForSource: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (Only Comix + Comix Plus Two): %+v", len(rows), rows)
	}
	got := bySeriesTitle(rows)
	// Only Comix has no fallback → goes dark. Comix Plus Two keeps two
	// alternatives, of which Flame (importance 30) is the take-over.
	assertSourceSeries(t, got, "Only Comix", true, 0, "")
	assertSourceSeries(t, got, "Comix Plus Two", false, 2, "Flame")
	if _, present := got["No Comix"]; present {
		t.Errorf("No Comix should not be listed — it does not carry source 7")
	}
}

// TestSeriesForSource_MatchesDiskNameRow proves the both-create-paths match: a
// series that carries the source as a DISK-ORIGIN row (provider stores the display
// NAME "Comix", provider_name empty) is found exactly like a live numeric row.
// This is the drift case the whole matcher exists for.
func TestSeriesForSource_MatchesDiskNameRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedSeriesWithProviders(ctx, t, client, "Disk Comix",
		provSpec{provider: "Comix", providerName: "", importance: 10}, // disk-origin row
		provSpec{provider: "9", providerName: "Flame", importance: 20})

	svc := series.NewService(client, t.TempDir(), 14)
	rows, err := svc.SeriesForSource(ctx, 7, "Comix")
	if err != nil {
		t.Fatalf("SeriesForSource: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (disk-origin Comix row must match): %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Title != "Disk Comix" || r.GoesDark || r.AlternativeCount != 1 || r.TopAlternative != "Flame" {
		t.Errorf("Disk Comix = %+v, want title=Disk Comix goesDark=false alternativeCount=1 topAlternative=Flame", r)
	}
}

// TestSeriesForSource_SameSourceBothPathsNotSelfAlternative proves a series that
// carries the source as BOTH a live row and a disk row (the same physical source
// under both identities) does not count one as the other's alternative: with only
// those two rows the series still goes dark.
func TestSeriesForSource_SameSourceBothPathsNotSelfAlternative(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedSeriesWithProviders(ctx, t, client, "Comix Twice",
		provSpec{provider: "7", providerName: "Comix", importance: 10}, // live
		provSpec{provider: "Comix", providerName: "", importance: 10})  // disk

	svc := series.NewService(client, t.TempDir(), 14)
	rows, err := svc.SeriesForSource(ctx, 7, "Comix")
	if err != nil {
		t.Fatalf("SeriesForSource: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	if !rows[0].GoesDark || rows[0].AlternativeCount != 0 {
		t.Errorf("Comix Twice = %+v, want goesDark=true alternativeCount=0 (same source is never its own alternative)", rows[0])
	}
}

// TestSeriesForSource_EmptyNameYieldsEmpty proves the belt-and-suspenders guard:
// with no resolvable display name the service returns an empty (non-nil) list
// rather than matching every disk-origin row.
func TestSeriesForSource_EmptyNameYieldsEmpty(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedSeriesWithProviders(ctx, t, client, "Only Comix",
		provSpec{provider: "7", providerName: "Comix", importance: 10})

	svc := series.NewService(client, t.TempDir(), 14)
	rows, err := svc.SeriesForSource(ctx, 7, "  ")
	if err != nil {
		t.Fatalf("SeriesForSource: %v", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Errorf("empty name should yield an empty non-nil list, got %+v", rows)
	}
}

// TestSeriesForSourceQueryCountIsSeriesCountIndependent is the NO-N+1 proof: the
// SQL read count for SeriesForSource must NOT grow with the number of matched
// series. It measures the reads (via the shared countingDriver) for a small
// carrying-set, seeds many more series carrying the same source, and measures
// again — the two counts must be identical and small (one series query + one
// providers eager-load), never a per-series lookup.
func TestSeriesForSourceQueryCountIsSeriesCountIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)

	seedCarrying := func(from, to int) {
		for i := from; i < to; i++ {
			seedSeriesWithProviders(ctx, t, seedClient, fmt.Sprintf("Carrier %02d", i),
				provSpec{provider: "7", providerName: "Comix", importance: 10},
				provSpec{provider: "9", providerName: "Flame", importance: 20})
		}
	}

	client, drv := newCountingClient(db)
	svc := series.NewService(client, t.TempDir(), 14)

	count := func(wantRows int) int64 {
		drv.queries.Store(0)
		rows, err := svc.SeriesForSource(ctx, 7, "Comix")
		if err != nil {
			t.Fatalf("SeriesForSource: %v", err)
		}
		if len(rows) != wantRows {
			t.Fatalf("SeriesForSource returned %d rows, want %d", len(rows), wantRows)
		}
		return drv.queries.Load()
	}

	seedCarrying(0, 3)
	small := count(3)
	seedCarrying(3, 15)
	large := count(15)

	if small != large {
		t.Errorf("N+1: SeriesForSource issued %d queries for 3 series but %d for 15 — the count must not scale with the matched-series count", small, large)
	}
	const maxQueries = 3
	if large > maxQueries {
		t.Errorf("SeriesForSource issued %d queries, want <= %d (one series query + one providers eager-load)", large, maxQueries)
	}
	t.Logf("queries: 3 series=%d, 15 series=%d", small, large)
}
