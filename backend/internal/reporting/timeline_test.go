package reporting_test

import (
	"context"
	"testing"
	"time"

	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/reporting"
)

// sumBuckets totals the success + failed counts across every bucket.
func sumBuckets(buckets []reporting.TimelineBucket) (success, failed int) {
	for _, b := range buckets {
		success += b.Success
		failed += b.Failed
	}
	return
}

// TestTimeline_HourBuckets proves hour bucketing groups events into distinct
// hourly slots and that the per-status counts sum to what was seeded.
func TestTimeline_HourBuckets(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	// Three distinct hours within the 24h window (3h apart avoids any tz-boundary
	// ambiguity): each hour has a known success/fail mix.
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})
	// Same hour as the one above (+5min, stays within the same clock hour so it
	// truncates to the same bucket regardless of the DB session timezone).
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1*time.Hour + 5*time.Minute)})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-4 * time.Hour)})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeDownload, stat: entsourceevent.StatusFailed, at: refNow.Add(-7 * time.Hour)})

	buckets, err := svc.Timeline(ctx, "A", reporting.BucketHour, reporting.Period24h, refNow)
	if err != nil {
		t.Fatalf("Timeline(hour): %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("hour buckets = %d, want 3 distinct hours (%+v)", len(buckets), buckets)
	}
	// Ascending order: oldest bucket first.
	for i := 1; i < len(buckets); i++ {
		if !buckets[i-1].Bucket.Before(buckets[i].Bucket) {
			t.Errorf("buckets not ascending: %v then %v", buckets[i-1].Bucket, buckets[i].Bucket)
		}
	}
	s, f := sumBuckets(buckets)
	if s != 2 || f != 2 {
		t.Errorf("bucket sums success/fail = %d/%d, want 2/2", s, f)
	}
}

// TestTimeline_DayBucketsAllSources proves day bucketing over the 7d window, the
// __all__ sentinel spanning sources, and period exclusion of an out-of-window
// event.
func TestTimeline_DayBucketsAllSources(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	// Three distinct days (48h apart) inside the 7d window, across two sources.
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * 24 * time.Hour)})
	seed(t, client, ev{key: "B", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-3 * 24 * time.Hour)})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-5 * 24 * time.Hour)})
	// Out of the 7d window — excluded.
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-10 * 24 * time.Hour)})

	buckets, err := svc.Timeline(ctx, reporting.AllSourcesKey, reporting.BucketDay, reporting.Period7d, refNow)
	if err != nil {
		t.Fatalf("Timeline(day,__all__): %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("day buckets = %d, want 3 (out-of-window excluded) (%+v)", len(buckets), buckets)
	}
	s, f := sumBuckets(buckets)
	if s != 2 || f != 1 {
		t.Errorf("bucket sums success/fail = %d/%d, want 2/1", s, f)
	}
}

// TestTimeline_Empty proves an empty window yields a non-nil, empty slice.
func TestTimeline_Empty(t *testing.T) {
	svc, _ := newService(t)
	buckets, err := svc.Timeline(context.Background(), reporting.AllSourcesKey, reporting.BucketHour, reporting.Period24h, refNow)
	if err != nil {
		t.Fatalf("Timeline(empty): %v", err)
	}
	if buckets == nil {
		t.Fatal("timeline must be non-nil even when empty")
	}
	if len(buckets) != 0 {
		t.Errorf("empty timeline len = %d, want 0", len(buckets))
	}
}
