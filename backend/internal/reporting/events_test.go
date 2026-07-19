package reporting_test

import (
	"context"
	"testing"
	"time"

	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/reporting"
)

// TestEvents_FilterByStatusAndType proves the feed applies the status + eventType
// filters (and returns the matching total alongside the page).
func TestEvents_FilterByStatusAndType(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-2 * time.Hour)})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeDownload, stat: entsourceevent.StatusFailed, at: refNow.Add(-3 * time.Hour)})

	failed := entsourceevent.StatusFailed
	feed, err := svc.Events(ctx, reporting.EventFilter{SourceKey: "A", Status: &failed, Limit: 50})
	if err != nil {
		t.Fatalf("Events(status=failed): %v", err)
	}
	if feed.Total != 2 || len(feed.Items) != 2 {
		t.Fatalf("status=failed total/items = %d/%d, want 2/2", feed.Total, len(feed.Items))
	}

	dl := entsourceevent.EventTypeDownload
	feed, err = svc.Events(ctx, reporting.EventFilter{SourceKey: "A", Status: &failed, EventType: &dl, Limit: 50})
	if err != nil {
		t.Fatalf("Events(status+type): %v", err)
	}
	if feed.Total != 1 || len(feed.Items) != 1 || feed.Items[0].EventType != entsourceevent.EventTypeDownload {
		t.Fatalf("status=failed&type=download = %+v, want the one download fail", feed)
	}
}

// TestEvents_AllSourcesSentinel proves the __all__ sentinel returns the GLOBAL
// feed across sources, while a concrete key scopes to one source.
func TestEvents_AllSourcesSentinel(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-1 * time.Hour)})
	seed(t, client, ev{key: "B", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-2 * time.Hour)})

	all, err := svc.Events(ctx, reporting.EventFilter{SourceKey: reporting.AllSourcesKey, Limit: 50})
	if err != nil {
		t.Fatalf("Events(__all__): %v", err)
	}
	if all.Total != 2 {
		t.Errorf("__all__ total = %d, want 2 (both sources)", all.Total)
	}

	one, err := svc.Events(ctx, reporting.EventFilter{SourceKey: "A", Limit: 50})
	if err != nil {
		t.Fatalf("Events(A): %v", err)
	}
	if one.Total != 1 || one.Items[0].SourceKey != "A" {
		t.Errorf("A-scoped = %+v, want just A", one)
	}
}

// TestEvents_Pagination proves the page/total split: total is the full match
// count, items is bounded by limit, and offset advances the newest-first page.
func TestEvents_Pagination(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	// 5 events, newest = i==4 (created at refNow-1h), oldest = i==0.
	for i := 0; i < 5; i++ {
		seed(t, client, ev{key: "A", id: string(rune('0' + i)), typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-time.Duration(5-i) * time.Hour)})
	}

	page1, err := svc.Events(ctx, reporting.EventFilter{SourceKey: "A", Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("Events page1: %v", err)
	}
	if page1.Total != 5 || len(page1.Items) != 2 {
		t.Fatalf("page1 total/items = %d/%d, want 5/2", page1.Total, len(page1.Items))
	}
	// Newest first: the two most recent (id "4" then "3").
	if page1.Items[0].SourceID != "4" || page1.Items[1].SourceID != "3" {
		t.Errorf("page1 order = %q,%q, want 4,3 (newest first)", page1.Items[0].SourceID, page1.Items[1].SourceID)
	}

	page2, err := svc.Events(ctx, reporting.EventFilter{SourceKey: "A", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("Events page2: %v", err)
	}
	if page2.Items[0].SourceID != "2" {
		t.Errorf("page2 first = %q, want 2 (offset advanced)", page2.Items[0].SourceID)
	}
}

// TestEvents_PreservesOptionalFields proves NULL optional columns stay nil and set
// ones round-trip (error message/category, items count, metadata).
func TestEvents_PreservesOptionalFields(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	seed(t, client, ev{
		key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusFailed, at: refNow.Add(-1 * time.Hour),
		errMsg: "cf challenge", errCat: "captcha", items: intptr(0), meta: map[string]string{"keyword": "solo"},
	})
	seed(t, client, ev{key: "A", typ: entsourceevent.EventTypeSearch, stat: entsourceevent.StatusSuccess, at: refNow.Add(-30 * time.Minute), items: intptr(12)})

	feed, err := svc.Events(ctx, reporting.EventFilter{SourceKey: "A", Limit: 50})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	// Newest first: the success (no error), then the failure.
	ok := feed.Items[0]
	if ok.ErrorMessage != nil || ok.ErrorCategory != nil {
		t.Errorf("success event should have nil error fields, got msg=%v cat=%v", ok.ErrorMessage, ok.ErrorCategory)
	}
	if ok.ItemsCount == nil || *ok.ItemsCount != 12 {
		t.Errorf("success itemsCount = %v, want 12", ok.ItemsCount)
	}
	fail := feed.Items[1]
	if fail.ErrorCategory == nil || *fail.ErrorCategory != "captcha" {
		t.Errorf("fail category = %v, want captcha", fail.ErrorCategory)
	}
	if fail.Metadata["keyword"] != "solo" {
		t.Errorf("fail metadata = %v, want keyword=solo", fail.Metadata)
	}
}
