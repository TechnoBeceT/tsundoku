package warmup_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
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

func TestWarmAll_CancellationIsNotASourceOutcome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		canceled bool
	}{
		{"canceled", context.Canceled, true},
		{"wrapped canceled", fmt.Errorf("warm stopped: %w", context.Canceled), true},
		{"deadline", context.DeadlineExceeded, false},
		{"upstream", errors.New("HTTP 522"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkWarmOutcome(t, tc.cause, tc.canceled)
		})
	}
}

func checkWarmOutcome(t *testing.T, cause error, canceled bool) {
	t.Helper()
	client := testdb.New(t)
	ctx := context.Background()
	metric := metrics.NewService(client)
	metric.Record(ctx, "1", "Source", time.Second, errors.New("previous useful error"))
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 5, SourcesCooldownIv: time.Hour})
	gate.RecordFailure(ctx, "Source", errors.New("previous useful error"), time.Now())
	beforeMetric := warmOutcomeJSON(t, client.SourceMetric.Query().OnlyX(ctx))
	beforeGate := warmOutcomeJSON(t, client.SourceCircuitState.Query().OnlyX(ctx))
	fc := &fakeClient{
		sources:     []sourceengine.Source{{ID: 1, Name: "Source", Lang: "en"}},
		popularErrs: map[int64]error{1: cause},
	}
	rec := &captureRecorder{}
	svc := warmup.NewService(fc, metric, settings.Static{WarmupSlow: 5000}, gate).WithEventRecorder(rec)
	n, err := svc.WarmAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(fc.calls) != 1 || warmed(t, client, "1") {
		t.Fatalf("unsuccessful warm counted or stamped: n=%d calls=%v", n, fc.calls)
	}
	m := client.SourceMetric.Query().OnlyX(ctx)
	g := client.SourceCircuitState.Query().OnlyX(ctx)
	if canceled {
		assertCanceledWarmOutcome(t, m, g, rec.all(), beforeMetric, beforeGate)
	} else {
		assertFailedWarmOutcome(t, m, g, rec.all(), cause)
	}
}

func warmOutcomeJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func assertCanceledWarmOutcome(t *testing.T, m *ent.SourceMetric, g *ent.SourceCircuitState, events []sourceevents.Event, beforeMetric, beforeGate string) {
	t.Helper()
	if after := warmOutcomeJSON(t, m); beforeMetric != after {
		t.Errorf("cancellation mutated source metrics: %s -> %s", beforeMetric, after)
	}
	if after := warmOutcomeJSON(t, g); beforeGate != after {
		t.Errorf("cancellation mutated breaker state: %s -> %s", beforeGate, after)
	}
	if len(events) != 0 {
		t.Errorf("cancellation emitted warm outcome: %+v", events)
	}
}

func assertFailedWarmOutcome(t *testing.T, m *ent.SourceMetric, g *ent.SourceCircuitState, events []sourceevents.Event, cause error) {
	t.Helper()
	if m.SearchCount != 2 || m.FailCount != 2 || m.LastError != cause.Error() {
		t.Errorf("genuine failure lost from metrics: %+v", m)
	}
	if g.ConsecutiveFailures != 2 {
		t.Errorf("genuine failure lost from breaker: %+v", g)
	}
	if len(events) != 1 || events[0].Status != sourceevents.StatusFailed || !errors.Is(events[0].Err, cause) {
		t.Errorf("genuine warm failure event lost: %+v", events)
	}
}
