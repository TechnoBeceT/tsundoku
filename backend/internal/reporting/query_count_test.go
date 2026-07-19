package reporting_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/reporting"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// countingDriver wraps an Ent SQL driver and counts every read query. Test-only:
// it PROVES the reporting rollups aggregate in SQL — the query count is bounded
// and does NOT grow with the number of sources/events (an in-memory load-all fold
// would still be one query, but a per-source N+1 join would balloon; this pins
// the shape and guards a future refactor from introducing one).
type countingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

// Query counts the read and delegates.
func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

// newCountingService builds a reporting.Service over the SAME test database whose
// reads are counted, so a test can assert the query count of a single call.
func newCountingService(t *testing.T) (*reporting.Service, *ent.Client, *countingDriver) {
	t.Helper()
	_, db := testdb.NewWithSQL(t)
	drv := &countingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := ent.NewClient(ent.Driver(drv))
	metricsSvc := metrics.NewService(client)
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 3, SourcesCooldownIv: 10 * time.Minute})
	return reporting.NewService(client, metricsSvc, gate), client, drv
}

// seedSourcesEvents seeds nSources sources, each with a search + a download event,
// so a per-source N+1 in the rollup would show up as query growth.
func seedSourcesEvents(t *testing.T, client *ent.Client, nSources int) {
	t.Helper()
	ctx := context.Background()
	for i := range nSources {
		key := fmt.Sprintf("src-%02d", i)
		for _, typ := range []entsourceevent.EventType{entsourceevent.EventTypeSearch, entsourceevent.EventTypeDownload} {
			if err := client.SourceEvent.Create().
				SetSourceKey(key).SetSourceID(fmt.Sprintf("%d", i)).SetSourceName(key).
				SetEventType(typ).SetStatus(entsourceevent.StatusSuccess).
				SetCreatedAt(refNow.Add(-1 * time.Hour)).
				Exec(ctx); err != nil {
				t.Fatalf("seed %q: %v", key, err)
			}
		}
	}
}

// TestSources_QueryCountIsSourceCountIndependent is the SQL-aggregation proof: the
// per-source rollup issues the SAME (small) number of reads for 2 sources as for
// 20 — the counts come out of the DB already grouped, never a per-source query.
func TestSources_QueryCountIsSourceCountIndependent(t *testing.T) {
	svc, client, drv := newCountingService(t)
	ctx := context.Background()

	seedSourcesEvents(t, client, 2)
	drv.queries.Store(0)
	if _, err := svc.Sources(ctx, reporting.Period24h, reporting.SortFailures, refNow); err != nil {
		t.Fatalf("Sources(2): %v", err)
	}
	small := drv.queries.Load()

	// Fresh DB via a second counting service so the 20-source run is isolated.
	svc2, client2, drv2 := newCountingService(t)
	seedSourcesEvents(t, client2, 20)
	drv2.queries.Store(0)
	if _, err := svc2.Sources(ctx, reporting.Period24h, reporting.SortFailures, refNow); err != nil {
		t.Fatalf("Sources(20): %v", err)
	}
	large := drv2.queries.Load()

	if small != large {
		t.Errorf("query count grew with source count: 2 sources = %d reads, 20 sources = %d reads (N+1)", small, large)
	}
	if large > 4 {
		t.Errorf("Sources issued %d reads, expected a small constant (rollup + identity + metrics + breaker)", large)
	}
}

// TestOverview_QueryCountIsEventCountIndependent proves the overview is likewise
// bounded: aggregating 20 sources' events costs the same reads as 2.
func TestOverview_QueryCountIsEventCountIndependent(t *testing.T) {
	svc, client, drv := newCountingService(t)
	ctx := context.Background()

	seedSourcesEvents(t, client, 2)
	drv.queries.Store(0)
	if _, err := svc.Overview(ctx, reporting.Period24h, refNow); err != nil {
		t.Fatalf("Overview(2): %v", err)
	}
	small := drv.queries.Load()

	svc2, client2, drv2 := newCountingService(t)
	seedSourcesEvents(t, client2, 20)
	drv2.queries.Store(0)
	if _, err := svc2.Overview(ctx, reporting.Period24h, refNow); err != nil {
		t.Fatalf("Overview(20): %v", err)
	}
	large := drv2.queries.Load()

	if small != large {
		t.Errorf("overview query count grew: 2 = %d, 20 = %d (N+1)", small, large)
	}
}
