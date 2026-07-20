// Package series_test — the no-N+1 proof for ListSeries' chapter-state rollup,
// extended to cover the unread tally: chapterRollups groups by (series_id,
// state, read) in the SAME aggregate query, never a second one or a per-series
// loop. Modelled on internal/downloads' countingDriver pattern
// (TestListQueryCountIsPageSizeIndependent).
package series_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// countingDriver wraps an Ent SQL driver and counts every read query issued
// through it (eager-loading sub-queries included — Ent runs them through the
// same driver). Test-only: it exists solely to PROVE ListSeries' query count is
// bounded, i.e. does not grow with the number of series on the page (no N+1).
type countingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

// Query counts the read and delegates.
func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

// newCountingClient builds a second Ent client over the SAME test database whose
// reads are counted.
func newCountingClient(db *sql.DB) (*ent.Client, *countingDriver) {
	drv := &countingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(drv)), drv
}

// seedManySeries creates n series, each with one provider and two downloaded
// chapters (one read, one unread) — so every row on the page needs both a
// provider resolution AND a non-trivial unread tally, the worst case for an N+1.
func seedManySeries(ctx context.Context, t *testing.T, client *ent.Client, n int) {
	t.Helper()
	for i := range n {
		s := client.Series.Create().
			SetTitle(fmt.Sprintf("Query Count Series %02d", i)).
			SetSlug(fmt.Sprintf("query-count-series-%02d", i)).
			SetCategoryID(catID(ctx, client, "Manga")).
			SaveX(ctx)
		client.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("src").SetImportance(10).SaveX(ctx)
		client.Chapter.Create().SetSeriesID(s.ID).SetChapterKey(fmt.Sprintf("qc-%d-1", i)).
			SetState(entchapter.StateDownloaded).SetRead(false).SaveX(ctx)
		client.Chapter.Create().SetSeriesID(s.ID).SetChapterKey(fmt.Sprintf("qc-%d-2", i)).
			SetState(entchapter.StateDownloaded).SetRead(true).SaveX(ctx)
	}
}

// TestListSeriesQueryCountIsSeriesCountIndependent is the NO-N+1 proof for the
// unread tally: it counts the SQL reads (via countingDriver) issued by one
// ListSeries call at page size 2 and again at page size 20. The counts must be
// IDENTICAL and small — an N+1 (a per-series unread lookup) would make the
// larger page cost many more queries.
func TestListSeriesQueryCountIsSeriesCountIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	seedManySeries(ctx, t, seedClient, 20)

	client, drv := newCountingClient(db)
	svc := series.NewService(client, t.TempDir(), 14)

	count := func(limit int) int64 {
		drv.queries.Store(0)
		page, err := svc.ListSeries(ctx, series.ListFilter{Limit: limit})
		if err != nil {
			t.Fatalf("ListSeries(limit=%d): %v", limit, err)
		}
		if len(page) != limit {
			t.Fatalf("ListSeries(limit=%d) returned %d items, want %d", limit, len(page), limit)
		}
		for _, sm := range page {
			if sm.ChapterCounts.Unread != 1 {
				t.Fatalf("series %s Unread = %d, want 1 (one read + one unread downloaded chapter)", sm.Title, sm.ChapterCounts.Unread)
			}
		}
		return drv.queries.Load()
	}

	small, large := count(2), count(20)
	if small != large {
		t.Errorf("N+1: ListSeries issued %d queries for a 2-item page but %d for a 20-item page — the query count must not scale with page size", small, large)
	}
	// Bounded shape: the series page (+ its providers/category eager loads) + the
	// grouped chapter-rollup aggregate + the QCAT-297 provider-upload-date
	// aggregate (both page-size independent). A generous ceiling still fails an N+1.
	const maxQueries = 7
	if large > maxQueries {
		t.Errorf("ListSeries issued %d queries for one page, want <= %d (bounded, page-size independent)", large, maxQueries)
	}
	t.Logf("queries: page(2)=%d page(20)=%d", small, large)
}
