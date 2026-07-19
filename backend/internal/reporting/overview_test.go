package reporting_test

import (
	"context"
	"testing"
	"time"

	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/reporting"
)

// TestOverview_KPIsAndBreakdown proves the KPI totals, success rate, active-source
// count, and the per-operation breakdown are all correct for the window — and are
// derived from a period-bounded aggregate (an out-of-window event is excluded).
func TestOverview_KPIsAndBreakdown(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	// In-window (last 24h of refNow): 3 searches (2 ok, 1 fail), 2 downloads (1 ok, 1 fail).
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-2 * time.Hour)})
	seed(t, client, ev{key: "B", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-3 * time.Hour), errCat: "captcha"})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeDownload, stat: entsourceevent.StatusSuccess, at: refNow.Add(-4 * time.Hour)})
	seed(t, client, ev{key: "B", typ: entsourceevent.EventTypeDownload, stat: entsourceevent.StatusFailed, at: refNow.Add(-5 * time.Hour), errCat: "rate_limit"})
	// Out of window (older than 24h) — must NOT be counted.
	seed(t, client, ev{key: "C", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-48 * time.Hour)})

	rep, err := svc.Overview(ctx, reporting.Period24h, refNow)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}

	assertEq(t, "totalEvents (out-of-window excluded)", rep.KPIs.TotalEvents, 5)
	assertEq(t, "successEvents", rep.KPIs.SuccessEvents, 3)
	assertEq(t, "failedEvents", rep.KPIs.FailedEvents, 2)
	assertEq(t, "successRate", rep.KPIs.SuccessRate, 3.0/5.0)
	assertEq(t, "activeSources (A,B; C out of window)", rep.KPIs.ActiveSources, 2)

	// Breakdown: search (3, sorted first as the biggest), download (2).
	if len(rep.EventsByType) != 2 {
		t.Fatalf("eventsByType len = %d, want 2 (%+v)", len(rep.EventsByType), rep.EventsByType)
	}
	assertEq(t, "top breakdown type", rep.EventsByType[0].EventType, entsourceevent.EventTypeSearch)
	assertEq(t, "search total", rep.EventsByType[0].Total, 3)
	assertEq(t, "search success", rep.EventsByType[0].Success, 2)
	assertEq(t, "search failed", rep.EventsByType[0].Failed, 1)
	assertEq(t, "second breakdown type", rep.EventsByType[1].EventType, entsourceevent.EventTypeDownload)
	assertEq(t, "download total", rep.EventsByType[1].Total, 2)
}

// TestOverview_LeaderboardsAndRecentErrors proves the slowest-source leaderboard
// (from metrics), the failing-source leaderboard (from the breaker), and the
// recent-errors preview (last failed events, newest first) are all populated.
func TestOverview_LeaderboardsAndRecentErrors(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	seedMetric(t, client, "1", "Fast", 500)
	seedMetric(t, client, "2", "Slow", 9000)

	// Two failing sources, "Older" failing longer than "Newer".
	tripBreaker(t, client, "Older", refNow.Add(-6*time.Hour), refNow.Add(10*time.Minute))
	tripBreaker(t, client, "Newer", refNow.Add(-2*time.Hour), refNow.Add(10*time.Minute))

	// Two failed events in window, plus one success (excluded from recentErrors).
	seed(t, client, ev{key: "Older", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-1 * time.Hour), errMsg: "cf challenge", errCat: "captcha"})
	seed(t, client, ev{key: "Newer", typ: entsourceevent.EventTypeDownload, stat: entsourceevent.StatusFailed, at: refNow.Add(-30 * time.Minute), errMsg: "429", errCat: "rate_limit"})
	seed(t, client, ev{key: "Older", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-20 * time.Minute)})

	rep, err := svc.Overview(ctx, reporting.Period24h, refNow)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}

	if len(rep.SlowestSources) != 2 || len(rep.FailingSources) != 2 || len(rep.RecentErrors) != 2 {
		t.Fatalf("lengths = slow %d / failing %d / recent %d, want 2/2/2", len(rep.SlowestSources), len(rep.FailingSources), len(rep.RecentErrors))
	}
	assertEq(t, "slowestSources[0]", rep.SlowestSources[0].SourceName, "Slow")
	assertEq(t, "failingSources[0] (longest-failing)", rep.FailingSources[0].SourceKey, "Older")
	assertTrue(t, "Older breaker should be cooling down at refNow", rep.FailingSources[0].IsCoolingDown)

	// recentErrors: only the two failures, newest first (Newer before Older).
	assertEq(t, "recentErrors[0] (newest first)", rep.RecentErrors[0].SourceKey, "Newer")
	if rep.RecentErrors[0].ErrorCategory == nil {
		t.Fatal("recentErrors[0] category should be set")
	}
	assertEq(t, "recentErrors[0] category", *rep.RecentErrors[0].ErrorCategory, "rate_limit")
}

// TestOverview_EmptyIsNonNil proves a window with no events yields zeroed KPIs
// and non-nil (empty) slices — never a nil slice the JSON layer would render null.
func TestOverview_EmptyIsNonNil(t *testing.T) {
	svc, _ := newService(t)
	rep, err := svc.Overview(context.Background(), reporting.Period7d, refNow)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if rep.KPIs.TotalEvents != 0 || rep.KPIs.SuccessRate != 0 {
		t.Errorf("empty KPIs = %+v, want zeroed", rep.KPIs)
	}
	if rep.EventsByType == nil || rep.SlowestSources == nil || rep.FailingSources == nil || rep.RecentErrors == nil {
		t.Error("all overview slices must be non-nil even when empty")
	}
}
