package download

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

// scheduleCountingDriver counts database reads while delegating to the real
// PostgreSQL driver. Writes are deliberately excluded: this test measures only
// candidate-resolution query growth.
type scheduleCountingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

func (d *scheduleCountingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

func newScheduleCountingClient(db *sql.DB) (*ent.Client, *scheduleCountingDriver) {
	drv := &scheduleCountingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(drv)), drv
}

type scheduleSeriesFixture struct {
	series    *ent.Series
	providers map[string]*ent.SeriesProvider
}

func seedScheduleSeries(
	ctx context.Context,
	t *testing.T,
	client *ent.Client,
	slug string,
	primary string,
) scheduleSeriesFixture {
	t.Helper()

	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	providers := make(map[string]*ent.SeriesProvider)
	for _, source := range []struct {
		provider   string
		importance int
	}{
		{provider: "599", importance: 60}, // paused: would otherwise win
		{provider: "spent-" + slug, importance: 50},
		{provider: "cooling-" + slug, importance: 40},
		{provider: primary, importance: 30},
		{provider: "fallback", importance: 20},
	} {
		providers[source.provider] = client.SeriesProvider.Create().
			SetSeries(s).
			SetProvider(source.provider).
			SetImportance(source.importance).
			SaveX(ctx)
	}
	return scheduleSeriesFixture{series: s, providers: providers}
}

func seedScheduleChapter(
	ctx context.Context,
	t *testing.T,
	client *ent.Client,
	fixture scheduleSeriesFixture,
	key string,
	number float64,
	now time.Time,
) *ent.Chapter {
	t.Helper()

	for provider, sp := range fixture.providers {
		create := client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(key).
			SetNumber(number).
			SetURL("https://" + provider + ".example/" + key)
		switch {
		case provider == "spent-"+fixture.series.Slug:
			create.SetAttempts(3)
		case provider == "cooling-"+fixture.series.Slug:
			create.SetNextAttemptAt(now.Add(time.Hour))
		}
		create.SaveX(ctx)
	}
	return client.Chapter.Create().
		SetSeries(fixture.series).
		SetChapterKey(key).
		SetNumber(number).
		SaveX(ctx)
}

func loadScheduleGroups(
	ctx context.Context,
	t *testing.T,
	d *Dispatcher,
	drv *scheduleCountingDriver,
	now time.Time,
) (map[string][]resolvedChapter, int64) {
	t.Helper()

	selections, err := chapter.WantedSelections(ctx, d.client, 100)
	if err != nil {
		t.Fatalf("WantedSelections: %v", err)
	}
	drv.queries.Store(0)
	groups := d.groupBySource(ctx, selections, 3, now, map[int64]bool{599: true})
	return groups, drv.queries.Load()
}

func candidateProviders(candidates []chapter.Candidate) []string {
	providers := make([]string, len(candidates))
	for i, candidate := range candidates {
		providers[i] = candidate.SeriesProvider.Provider
	}
	return providers
}

func assertScheduleGroup(t *testing.T, groups map[string][]resolvedChapter, source string, wantChapters []*ent.Chapter, wantProviders []string) {
	t.Helper()
	got := groups[source]
	if len(got) != len(wantChapters) {
		t.Fatalf("group %q length = %d, want %d", source, len(got), len(wantChapters))
	}
	for i, wantChapter := range wantChapters {
		assertScheduleResolved(t, source, i, got[i], wantChapter, wantProviders)
	}
}

func assertScheduleResolved(t *testing.T, source string, position int, got resolvedChapter, wantChapter *ent.Chapter, wantProviders []string) {
	t.Helper()
	if got.chapterID != wantChapter.ID {
		t.Errorf("group %q position %d chapter = %s, want %s", source, position, got.chapterID, wantChapter.ID)
	}
	if got.selectedState != wantChapter.State {
		t.Errorf("group %q position %d state = %s, want %s", source, position, got.selectedState, wantChapter.State)
	}
	if got.workGeneration == "" {
		t.Errorf("group %q position %d has empty chapter generation", source, position)
	}
	gotProviders := candidateProviders(got.cands)
	if !slices.Equal(gotProviders, wantProviders) {
		t.Errorf("group %q position %d candidates = %v, want %v", source, position, gotProviders, wantProviders)
	}
	for candidateIndex, candidate := range got.cands {
		if candidate.Generation == "" {
			t.Errorf("group %q position %d candidate %d has empty provider-chapter generation", source, position, candidateIndex)
		}
	}
}

// TestGroupBySource_BulkCandidatesKeepExactSelectionAndConstantQueryCount is the
// wanted-scheduler no-N+1 proof. The small fixture pins the exact primary-source
// groups, round-robin chapter order, best-first fallback order, and both MVCC
// generations. Growing the selected batch threefold must not grow candidate-load
// reads; a per-chapter RankedLiveCandidates call makes this test fail with a
// deterministic linear query slope.
func TestGroupBySource_BulkCandidatesKeepExactSelectionAndConstantQueryCount(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	client, drv := newScheduleCountingClient(db)
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	seriesA := seedScheduleSeries(ctx, t, seedClient, "schedule-a", "alpha")
	seriesB := seedScheduleSeries(ctx, t, seedClient, "schedule-b", "alpha")
	seriesC := seedScheduleSeries(ctx, t, seedClient, "schedule-c", "gamma")
	a1 := seedScheduleChapter(ctx, t, seedClient, seriesA, "a-1", 1, now)
	a2 := seedScheduleChapter(ctx, t, seedClient, seriesA, "a-2", 2, now)
	b1 := seedScheduleChapter(ctx, t, seedClient, seriesB, "b-1", 3, now)
	c1 := seedScheduleChapter(ctx, t, seedClient, seriesC, "c-1", 4, now)

	d := &Dispatcher{client: client}
	groups, smallReads := loadScheduleGroups(ctx, t, d, drv, now)

	if len(groups) != 2 {
		t.Fatalf("groups = %v, want exactly alpha and gamma", groups)
	}
	// Raw wanted order is A1,A2,B1; source-alpha output must remain the existing
	// round-robin A1,B1,A2, while each item retains alpha then fallback.
	assertScheduleGroup(t, groups, "alpha", []*ent.Chapter{a1, b1, a2}, []string{"alpha", "fallback"})
	assertScheduleGroup(t, groups, "gamma", []*ent.Chapter{c1}, []string{"gamma", "fallback"})

	// Grow the SAME selected workload from 4 to 12 chapters. Every added chapter
	// has multiple offered sources and a live primary, so no-candidate handling
	// cannot add unrelated reads to the measurement.
	for i := 5; i <= 12; i++ {
		seedScheduleChapter(ctx, t, seedClient, seriesA, fmt.Sprintf("a-%d", i), float64(i), now)
	}
	_, largeReads := loadScheduleGroups(ctx, t, d, drv, now)
	t.Logf("candidate-load reads: 4 chapters=%d, 12 chapters=%d", smallReads, largeReads)

	if smallReads != largeReads {
		t.Errorf("candidate-load reads scale with batch size: 4 chapters=%d, 12 chapters=%d", smallReads, largeReads)
	}
	const maxBulkReads = 2 // ProviderChapter query + SeriesProvider eager-load.
	if largeReads > maxBulkReads {
		t.Errorf("candidate loading issued %d reads, want <= %d bounded bulk reads", largeReads, maxBulkReads)
	}
}
