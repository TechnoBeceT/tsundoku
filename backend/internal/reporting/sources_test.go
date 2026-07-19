package reporting_test

import (
	"context"
	"testing"
	"time"

	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/reporting"
)

// findReport returns the rollup for a source key, or fails.
func findReport(t *testing.T, reports []reporting.SourceReport, key string) reporting.SourceReport {
	t.Helper()
	for _, r := range reports {
		if r.SourceKey == key {
			return r
		}
	}
	t.Fatalf("no report for source %q in %+v", key, reports)
	return reporting.SourceReport{}
}

// TestSources_RollupJoinsAndCounts proves the per-source rollup tallies overall +
// per-type counts from the event log, joins EWMA latency (metrics) and the
// failure streak (breaker) by canonical key, and folds the freshest identity.
func TestSources_RollupJoinsAndCounts(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	// Source "Comix": 2 search ok, 1 search fail, 1 download fail.
	seed(t, client, ev{key: "Comix", id: "7", name: "Comix", lang: "en", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})
	seed(t, client, ev{key: "Comix", id: "7", name: "Comix", lang: "en", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-2 * time.Hour)})
	seed(t, client, ev{key: "Comix", id: "7", name: "Comix", lang: "en", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-3 * time.Hour)})
	seed(t, client, ev{key: "Comix", id: "7", name: "Comix", lang: "en", typ: entsourceevent.EventTypeDownload, stat: entsourceevent.StatusFailed, at: refNow.Add(-4 * time.Hour)})
	// Source "Asura": 1 search ok.
	seed(t, client, ev{key: "Asura", id: "9", name: "Asura", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})

	seedMetric(t, client, "7", "Comix", 4200)
	tripBreaker(t, client, "Comix", refNow.Add(-5*time.Hour), refNow.Add(10*time.Minute))

	reports, err := svc.Sources(ctx, reporting.Period24h, reporting.SortEvents, refNow)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports len = %d, want 2", len(reports))
	}

	comix := findReport(t, reports, "Comix")
	assertEq(t, "Comix total", comix.TotalEvents, 4)
	assertEq(t, "Comix success", comix.SuccessEvents, 2)
	assertEq(t, "Comix failed", comix.FailedEvents, 2)
	assertEq(t, "Comix sourceId", comix.SourceID, "7")
	assertEq(t, "Comix language", comix.Language, "en")
	assertEq(t, "Comix ewma (metrics join)", comix.EwmaLatencyMs, 4200)
	assertEq(t, "Comix consecutiveFailures (breaker join)", comix.ConsecutiveFailures, 3)
	assertTrue(t, "Comix should be failing (breaker join)", comix.FailingSince != nil)
	assertTrue(t, "Comix should be cooling down", comix.IsCoolingDown)

	// Per-type: search (3 total: 2 ok/1 fail), download (1 fail). Sorted total desc.
	if len(comix.ByType) != 2 {
		t.Fatalf("Comix byType len = %d, want 2 (%+v)", len(comix.ByType), comix.ByType)
	}
	assertEq(t, "Comix byType[0] type", comix.ByType[0].EventType, entsourceevent.EventTypeSearch)
	assertEq(t, "Comix search total", comix.ByType[0].Total, 3)
	assertEq(t, "Comix search success", comix.ByType[0].Success, 2)
	assertEq(t, "Comix search failed", comix.ByType[0].Failed, 1)
}

// TestSources_SortOrders proves each sort key orders the rollup correctly.
func TestSources_SortOrders(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	// "Busy": many events, few fails, fast. "Broken": few events, all fail. "Slow": medium, slow latency.
	for i := 0; i < 5; i++ {
		seed(t, client, ev{key: "Busy", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-time.Duration(i+1) * time.Hour)})
	}
	seed(t, client, ev{key: "Broken", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-1 * time.Hour)})
	seed(t, client, ev{key: "Broken", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-2 * time.Hour)})
	seed(t, client, ev{key: "Slow", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})

	seedMetric(t, client, "s", "Slow", 12000)
	seedMetric(t, client, "b", "Busy", 300)

	byFailures, err := svc.Sources(ctx, reporting.Period24h, reporting.SortFailures, refNow)
	if err != nil {
		t.Fatalf("Sources(failures): %v", err)
	}
	if byFailures[0].SourceKey != "Broken" {
		t.Errorf("sort=failures first = %q, want Broken", byFailures[0].SourceKey)
	}

	byEvents, err := svc.Sources(ctx, reporting.Period24h, reporting.SortEvents, refNow)
	if err != nil {
		t.Fatalf("Sources(events): %v", err)
	}
	if byEvents[0].SourceKey != "Busy" {
		t.Errorf("sort=events first = %q, want Busy", byEvents[0].SourceKey)
	}

	byLatency, err := svc.Sources(ctx, reporting.Period24h, reporting.SortLatency, refNow)
	if err != nil {
		t.Fatalf("Sources(latency): %v", err)
	}
	if byLatency[0].SourceKey != "Slow" {
		t.Errorf("sort=latency first = %q, want Slow", byLatency[0].SourceKey)
	}
}
