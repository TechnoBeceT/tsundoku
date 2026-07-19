package imports_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// eventCaptureRecorder is a sourceevents.Recorder test double capturing every
// logged event. Search logs events in a BACKGROUND goroutine, so reads poll.
type eventCaptureRecorder struct {
	mu     sync.Mutex
	events []sourceevents.Event
}

func (r *eventCaptureRecorder) Log(_ context.Context, event sourceevents.Event) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *eventCaptureRecorder) LogBatch(_ context.Context, events []sourceevents.Event) {
	r.mu.Lock()
	r.events = append(r.events, events...)
	r.mu.Unlock()
}

// waitForEvents polls until at least n events are captured (background logging)
// or a short deadline elapses, returning a snapshot copy.
func (r *eventCaptureRecorder) waitForEvents(t *testing.T, n int) []sourceevents.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.events)
		snap := append([]sourceevents.Event(nil), r.events...)
		r.mu.Unlock()
		if got >= n {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d event(s); got %d", n, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSearch_LogsSearchEvents proves Search logs one `search` audit event per
// source that ran (success AND failure), carrying the query keyword as metadata
// and the failing source's cause.
func TestSearch_LogsSearchEvents(t *testing.T) {
	t.Parallel()

	boom := errors.New("cloudflare challenge timed out")
	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "OK", Lang: "en"},
			{ID: 2, Name: "Bad", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Manga"}}},
		},
		searchErrs: map[int64]error{2: boom},
	}
	rec := &eventCaptureRecorder{}
	svc := imports.NewService(fc, nil, nil, "", testSearchTimeout, nil).WithEventRecorder(rec)

	if _, err := svc.Search(context.Background(), "solo", nil); err != nil {
		t.Fatalf("Search: %v", err)
	}

	events := rec.waitForEvents(t, 2)
	if len(events) != 2 {
		t.Fatalf("logged %d search events, want exactly 2", len(events))
	}
	byKey := map[string]sourceevents.Event{}
	for _, e := range events {
		if e.Type != sourceevents.EventSearch {
			t.Fatalf("event type = %q, want search", e.Type)
		}
		if e.Metadata["keyword"] != "solo" {
			t.Fatalf("event keyword metadata = %q, want 'solo'", e.Metadata["keyword"])
		}
		byKey[e.SourceKey] = e
	}
	if got := byKey["OK"]; got.Status != sourceevents.StatusSuccess || got.SourceID != "1" {
		t.Fatalf("OK search event = %+v, want success/id=1", got)
	}
	if got := byKey["Bad"]; got.Status != sourceevents.StatusFailed || got.Err == nil {
		t.Fatalf("Bad search event = %+v, want failed with cause", got)
	}
}
