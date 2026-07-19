package warmup_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// captureRecorder records logged source events for assertion (safe for
// concurrent use — warmOne logs synchronously, but the interface allows either).
type captureRecorder struct {
	mu     sync.Mutex
	events []sourceevents.Event
}

func (c *captureRecorder) Log(_ context.Context, event sourceevents.Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *captureRecorder) LogBatch(_ context.Context, events []sourceevents.Event) {
	c.mu.Lock()
	c.events = append(c.events, events...)
	c.mu.Unlock()
}

func (c *captureRecorder) all() []sourceevents.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sourceevents.Event(nil), c.events...)
}

// TestWarmEvents_SuccessAndFailure proves warmOne logs one `warm` event per
// source with the right status/source_key, on both success and failure.
func TestWarmEvents_SuccessAndFailure(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "EN", Lang: "en"},
			{ID: 2, Name: "KO", Lang: "ko"},
		},
		popularErrs: map[int64]error{2: errors.New("cloudflare challenge")},
	}
	rec := &captureRecorder{}
	svc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil).
		WithEventRecorder(rec)

	if _, err := svc.WarmAll(ctx); err != nil {
		t.Fatalf("WarmAll: %v", err)
	}

	events := rec.all()
	if len(events) != 2 {
		t.Fatalf("got %d warm events, want 2", len(events))
	}
	byKey := map[string]sourceevents.Event{}
	for _, e := range events {
		if e.Type != sourceevents.EventWarm {
			t.Fatalf("event type = %q, want warm", e.Type)
		}
		byKey[e.SourceKey] = e
	}
	if got := byKey["EN"]; got.Status != sourceevents.StatusSuccess || got.SourceID != "1" || got.Language != "en" {
		t.Fatalf("EN warm event = %+v, want success/id=1/lang=en", got)
	}
	if got := byKey["KO"]; got.Status != sourceevents.StatusFailed || got.Err == nil {
		t.Fatalf("KO warm event = %+v, want failed with cause", got)
	}
}
