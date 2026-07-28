package downloads_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// statementCountingDriver wraps an Ent SQL driver and counts every statement it
// issues — reads AND writes. Test-only: it PROVES the bulk re-download selects and
// mutates in SQL, so its statement count is a small constant that does NOT grow
// with the number of matched chapters. A per-chapter (or per-series) loop would
// show up immediately as growth.
type statementCountingDriver struct {
	dialect.Driver
	statements atomic.Int64
}

// Query counts the read and delegates.
func (d *statementCountingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.statements.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

// Exec counts the write and delegates.
func (d *statementCountingDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.statements.Add(1)
	return d.Driver.Exec(ctx, query, args, v)
}

// Tx wraps the transaction so statements issued inside it are counted too — the
// bulk apply does all of its writing in one tx, so an uncounted tx would make the
// measurement meaningless.
func (d *statementCountingDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &countingTx{Tx: tx, driver: d}, nil
}

// countingTx counts every statement issued inside a transaction against its
// parent driver's counter.
type countingTx struct {
	dialect.Tx
	driver *statementCountingDriver
}

// Query counts the in-transaction read and delegates.
func (t *countingTx) Query(ctx context.Context, query string, args, v any) error {
	t.driver.statements.Add(1)
	return t.Tx.Query(ctx, query, args, v)
}

// Exec counts the in-transaction write and delegates.
func (t *countingTx) Exec(ctx context.Context, query string, args, v any) error {
	t.driver.statements.Add(1)
	return t.Tx.Exec(ctx, query, args, v)
}

// newStatementCountingClient builds an Ent client over a fresh test database whose every
// statement is counted.
func newStatementCountingClient(t *testing.T) (*ent.Client, *statementCountingDriver) {
	t.Helper()
	_, db := testdb.NewWithSQL(t)
	drv := &statementCountingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(drv)), drv
}

// bulkChapterKey is the chapter_key of the i-th chapter of every seeded series.
// The keys are deliberately SHARED across series: resetProviderChapters folds one
// (series, keys) clause per series into a single OR'd update, and a shared key is
// what makes that per-series scoping load-bearing rather than decorative.
func bulkChapterKey(i int) string { return fmt.Sprintf("bulk-%04d", i) }

// seedBulkSeries seeds `series` series, each carrying ONE provider of the named
// source with `perSeries` downloaded chapters written after the cutoff and a feed
// row per chapter holding a spent budget (attempts=3).
//
// Both dimensions vary between the slope test's two runs on purpose. The bulk
// re-download's per-CHAPTER work was already set-wise; the per-SERIES work was not
// — resetProviderChapters used to issue one UPDATE per series — so a fixture with a
// single series would pin only half of what the path promises.
func seedBulkSeries(ctx context.Context, t *testing.T, client *ent.Client, source string, series, perSeries int) {
	t.Helper()
	written := cutoff.Add(time.Hour)
	for s := range series {
		row := client.Series.Create().
			SetTitle(fmt.Sprintf("%s Saga %d", source, s)).
			SetSlug(fmt.Sprintf("%s-saga-%d", strings.ToLower(source), s)).
			SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
		sp := client.SeriesProvider.Create().
			SetSeriesID(row.ID).SetProvider("42").SetProviderName(source).SetLanguage("en").
			SetImportance(30).SaveX(ctx)

		for i := range perSeries {
			key := bulkChapterKey(i)
			client.Chapter.Create().
				SetSeriesID(row.ID).SetChapterKey(key).
				SetState(entchapter.StateDownloaded).
				SetSatisfiedByProviderID(sp.ID).
				SetDownloadDate(written).
				SetFilename(key + ".cbz").
				SaveX(ctx)
			client.ProviderChapter.Create().
				SetSeriesProviderID(sp.ID).SetChapterKey(key).SetAttempts(3).SaveX(ctx)
		}
	}
}

// controlBudgetsIntact asserts that every feed row of a source the filter does NOT
// name still carries its spent budget. Those rows share their chapter_keys with the
// swept series, so this is the cross-series leak check: the OR'd reset must scope
// each key set to its own series.
func controlBudgetsIntact(ctx context.Context, t *testing.T, client *ent.Client, source string) {
	t.Helper()
	rows := client.ProviderChapter.Query().
		Where(entproviderchapter.HasSeriesProviderWith(entseriesprovider.ProviderNameEQ(source))).
		AllX(ctx)
	if len(rows) == 0 {
		t.Fatalf("control source %q seeded no feed rows — the leak check would be vacuous", source)
	}
	for _, pc := range rows {
		if pc.Attempts == 0 {
			t.Errorf("control source %q had %s reset: a shared chapter_key leaked the reset across series", source, pc.ChapterKey)
		}
	}
}

// TestRedownload_StatementCountIsSetSizeIndependent is the no-N+1 proof for the
// bulk re-download: previewing AND applying over 200 matched chapters spread across
// 40 series costs the SAME number of SQL statements as 6 chapters across 2 series.
//
// BOTH dimensions vary on purpose. A per-chapter loop is the obvious N+1, but the
// per-SERIES loop is the one this path actually had (resetProviderChapters issued
// one UPDATE per series before it folded the clauses into a single OR'd predicate),
// and a single-series fixture is blind to it. At the real remediation sizes —
// thousands of chapters across hundreds of series — either shape would be unusable.
//
// A control source sharing the swept chapter_keys is seeded alongside, so the run
// also proves the OR'd reset stays scoped per series.
func TestRedownload_StatementCountIsSetSizeIndependent(t *testing.T) {
	ctx := context.Background()

	measure := func(series, perSeries int) int64 {
		client, drv := newStatementCountingClient(t)
		seedBulkSeries(ctx, t, client, "Comix", series, perSeries)
		seedBulkSeries(ctx, t, client, "Untouched", series, perSeries)
		svc := downloads.NewService(client)
		drv.statements.Store(0)
		if _, err := svc.RedownloadPreview(ctx, comixFilter()); err != nil {
			t.Fatalf("RedownloadPreview(%dx%d): %v", series, perSeries, err)
		}
		got, err := svc.RedownloadAll(ctx, comixFilter())
		if err != nil {
			t.Fatalf("RedownloadAll(%dx%d): %v", series, perSeries, err)
		}
		if want := series * perSeries; got != want {
			t.Fatalf("RedownloadAll(%dx%d) requeued %d; want %d", series, perSeries, got, want)
		}
		statements := drv.statements.Load()
		controlBudgetsIntact(ctx, t, client, "Untouched")
		return statements
	}

	small := measure(2, 3)
	large := measure(40, 5)

	if small != large {
		t.Errorf("statement count grew with the matched set: 2x3 = %d, 40x5 = %d (N+1)", small, large)
	}
	if large > 8 {
		t.Errorf("bulk re-download issued %d statements, expected a small constant (count + select + two updates + tx bookkeeping)", large)
	}
}
