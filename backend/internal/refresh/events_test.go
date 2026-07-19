package refresh_test

import (
	"context"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sse"
)

// refreshEventRecorder captures logged source events (refresh flushes its batch
// synchronously after the sweep, so no polling is needed).
type refreshEventRecorder struct {
	mu     sync.Mutex
	events []sourceevents.Event
}

func (r *refreshEventRecorder) Log(_ context.Context, event sourceevents.Event) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *refreshEventRecorder) LogBatch(_ context.Context, events []sourceevents.Event) {
	r.mu.Lock()
	r.events = append(r.events, events...)
	r.mu.Unlock()
}

func (r *refreshEventRecorder) all() []sourceevents.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sourceevents.Event(nil), r.events...)
}

// TestRefreshEvents_SuccessAndFailure proves each sweep logs one `refresh` event
// per source-manga group — success for a source that fetched, failed for one
// that errored — with the right source_key/source_id and items_count.
func TestRefreshEvents_SuccessAndFailure(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const (
		okSource, okURL   = 10, "/manga/ok"
		badSource, badURL = 20, "/manga/bad"
	)
	fc := enginefake.New(enginefake.WithChapters(okSource, okURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
		{Number: num(2), URL: "u2"},
	}))
	failing := &partialFailClient{Client: fc, failURL: badURL}

	seedProviderNamed(t, ctx, db, "ok", okSource, "OK Source", okURL)
	seedProviderNamed(t, ctx, db, "bad", badSource, "Bad Source", badURL)

	rec := &refreshEventRecorder{}
	ing := ingest.NewIngest(failing, db)
	svc := refresh.NewService(db, ing, sse.NewHub(), settings.Static{Concurrency: 4}, nil).
		WithEventRecorder(rec)

	if _, err := svc.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	assertRefreshEvents(t, rec.all())
}

// assertRefreshEvents checks the sweep logged exactly two `refresh` events: a
// success for "OK Source" (with items_count) and a failure for "Bad Source".
func assertRefreshEvents(t *testing.T, events []sourceevents.Event) {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("got %d refresh events, want 2 (one per group)", len(events))
	}
	byKey := map[string]sourceevents.Event{}
	for _, e := range events {
		if e.Type != sourceevents.EventRefresh {
			t.Fatalf("event type = %q, want refresh", e.Type)
		}
		byKey[e.SourceKey] = e
	}
	ok := byKey["OK Source"]
	if ok.Status != sourceevents.StatusSuccess || ok.SourceID != "10" {
		t.Fatalf("ok refresh event = %+v, want success/id=10", ok)
	}
	if ok.ItemsCount == nil || *ok.ItemsCount != 2 {
		t.Fatalf("ok refresh items_count = %v, want 2", ok.ItemsCount)
	}
	if bad := byKey["Bad Source"]; bad.Status != sourceevents.StatusFailed || bad.Err == nil {
		t.Fatalf("bad refresh event = %+v, want failed with cause", bad)
	}
}

// seedProviderNamed creates a monitored series with one live provider carrying a
// display name (so the refresh event's source_key is that name, mirroring real
// ingest which always sets provider_name).
func seedProviderNamed(t *testing.T, ctx context.Context, db *ent.Client, title string, sourceID int64, name, mangaURL string) {
	t.Helper()
	s := db.Series.Create().SetTitle(title).SetSlug(disk.Slugify(title)).SetMonitored(true).SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(providerKey(sourceID)).
		SetProviderName(name).
		SetURL(mangaURL).
		SetImportance(10).
		SaveX(ctx)
}
