// Package refresh_test exercises the M5 discovery sweep against an ephemeral
// Postgres (testdb) and an in-process fake suwayomi.Client — no JVM, no network.
package refresh_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fakeClient implements suwayomi.Client. Only FetchChapters is exercised by the
// refresh sweep; the rest satisfy the interface. chaptersByManga maps a Suwayomi
// manga id to the chapter list returned by FetchChapters; failManga forces an
// error for a given manga id (to test partial-failure resilience).
type fakeClient struct {
	chaptersByManga map[int][]suwayomi.Chapter
	failManga       map[int]bool
}

func (f *fakeClient) Sources(context.Context) ([]suwayomi.Source, error) { return nil, nil }
func (f *fakeClient) Search(context.Context, string, string) ([]suwayomi.Manga, error) {
	return nil, nil
}
func (f *fakeClient) Browse(context.Context, string, suwayomi.BrowseType, int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (f *fakeClient) FetchChapters(_ context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	if f.failManga[mangaID] {
		return nil, errors.New("source offline")
	}
	return f.chaptersByManga[mangaID], nil
}
func (f *fakeClient) MangaChapters(context.Context, int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) MangaMeta(context.Context, int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeClient) ChapterPages(context.Context, int) ([]string, error)       { return nil, nil }
func (f *fakeClient) PageBytes(context.Context, string) ([]byte, string, error) { return nil, "", nil }
func (f *fakeClient) ServerSettings(context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeClient) SetServerSettings(context.Context, suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeClient) Extensions(context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (f *fakeClient) SetExtensionState(context.Context, string, suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeClient) FetchExtensions(context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (f *fakeClient) ExtensionRepos(context.Context) ([]string, error)              { return nil, nil }
func (f *fakeClient) SetExtensionRepos(context.Context, []string) error             { return nil }

// num returns a pointer to a float64 chapter number (Suwayomi's wire shape).
func num(n float64) *float64 { return &n }

// newSvc builds a refresh.Service over the given client + fake.
func newSvc(t *testing.T, db *ent.Client, fc *fakeClient) *refresh.Service {
	t.Helper()
	ingest := suwayomi.NewIngest(fc, db)
	return refresh.NewService(db, ingest, sse.NewHub(), settings.Static{Concurrency: 4})
}

// seedMonitoredSeries creates a monitored series with one provider (suwayomiID),
// no chapters yet. Returns the series provider id for later assertions.
func seedMonitoredSeries(t *testing.T, ctx context.Context, db *ent.Client, title, provider string, suwayomiID int) (*ent.Series, *ent.SeriesProvider) {
	t.Helper()
	// Slug MUST equal disk.Slugify(title): AddSeries upserts the series by that
	// slug, so a mismatch would make refresh create a SECOND series and the
	// provider assertions below would read an empty row.
	s := db.Series.Create().SetTitle(title).SetSlug(disk.Slugify(title)).SetMonitored(true).SaveX(ctx)
	sp := db.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetSuwayomiID(suwayomiID).SetImportance(10).SaveX(ctx)
	return s, sp
}

func TestRefreshAll_DiscoversNewChapters(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{
		77: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}},
	}}
	_, sp := seedMonitoredSeries(t, ctx, db, "alpha", "mangadex", 77)

	// First sweep: discovers chapter 1.
	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.SeriesRefreshed != 1 || res.ProvidersRefreshed != 1 || res.NewChapters != 1 {
		t.Fatalf("first sweep = %+v, want series=1 providers=1 new=1", res)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 1 {
		t.Fatalf("chapter count = %d, want 1", n)
	}
	wanted := db.Chapter.Query().Where(entchapter.StateEQ(entchapter.StateWanted)).CountX(ctx)
	if wanted != 1 {
		t.Errorf("wanted chapters = %d, want 1", wanted)
	}

	// Source publishes chapter 2.
	fc.chaptersByManga[77] = append(fc.chaptersByManga[77], suwayomi.Chapter{ID: 2, Index: 1, Number: num(2), URL: "u2"})
	res2, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll 2: %v", err)
	}
	if res2.NewChapters != 1 {
		t.Errorf("second sweep NewChapters = %d, want 1", res2.NewChapters)
	}
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).CountX(ctx); n != 2 {
		t.Errorf("provider chapters = %d, want 2", n)
	}
}

func TestRefreshAll_SkipsUnmonitored(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{99: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}}}}

	s := db.Series.Create().SetTitle("beta").SetSlug("beta").SetMonitored(false).SaveX(ctx)
	db.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetSuwayomiID(99).SaveX(ctx)

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.SeriesRefreshed != 0 || res.NewChapters != 0 {
		t.Errorf("res = %+v, want series=0 new=0 (unmonitored skipped)", res)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 0 {
		t.Errorf("chapter count = %d, want 0", n)
	}
}

func TestRefreshAll_SkipsUnknownSuwayomiID(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{}}

	s := db.Series.Create().SetTitle("gamma").SetSlug("gamma").SetMonitored(true).SaveX(ctx)
	// suwayomi_id defaults to 0 (unknown) — provider must be skipped, no fetch.
	db.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SaveX(ctx)

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.ProvidersRefreshed != 0 || res.Errors != 0 {
		t.Errorf("res = %+v, want providers=0 errors=0 (unknown id skipped, not failed)", res)
	}
}

func TestRefreshAll_PartialFailureContinues(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{
		chaptersByManga: map[int][]suwayomi.Chapter{10: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}}},
		failManga:       map[int]bool{20: true},
	}
	seedMonitoredSeries(t, ctx, db, "ok", "src-ok", 10)
	seedMonitoredSeries(t, ctx, db, "bad", "src-bad", 20)

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll must return nil error on partial failure, got %v", err)
	}
	if res.ProvidersRefreshed != 1 || res.Errors != 1 {
		t.Errorf("res = %+v, want providers=1 errors=1", res)
	}
}

func TestRefreshAll_PreservesImportance_And_NeverDeletes(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{
		5: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}, {ID: 2, Index: 1, Number: num(2), URL: "u2"}},
	}}
	_, sp := seedMonitoredSeries(t, ctx, db, "delta", "mangadex", 5) // importance 10

	if _, err := newSvc(t, db, fc).RefreshAll(ctx); err != nil {
		t.Fatalf("first RefreshAll: %v", err)
	}
	// Owner re-ranks: bump importance to 50.
	db.SeriesProvider.UpdateOne(sp).SetImportance(50).ExecX(ctx)

	// Source DROPS chapter 2 from its listing (never-delete must keep it).
	fc.chaptersByManga[5] = fc.chaptersByManga[5][:1]
	if _, err := newSvc(t, db, fc).RefreshAll(ctx); err != nil {
		t.Fatalf("second RefreshAll: %v", err)
	}

	got := db.SeriesProvider.GetX(ctx, sp.ID)
	if got.Importance != 50 {
		t.Errorf("importance = %d, want 50 (refresh must not reset it)", got.Importance)
	}
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).CountX(ctx); n != 2 {
		t.Errorf("provider chapters = %d, want 2 (dropped chapter must NOT be pruned)", n)
	}
}

// TestRefreshAll_EmitsSSEEvents verifies that RefreshAll broadcasts the
// expected SSE events to subscribers. It builds the hub explicitly (rather
// than using newSvc) so the subscriber channel is reachable for assertions.
// The test checks both event types and the monitored-count field on
// refresh.start, ensuring that a future regression (missing broadcast call or
// wrong payload) causes a clear test failure rather than a silent pass.
func TestRefreshAll_EmitsSSEEvents(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{
		42: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}},
	}}
	seedMonitoredSeries(t, ctx, db, "echo", "mangadex", 42)

	hub := sse.NewHub()
	svc := refresh.NewService(db, suwayomi.NewIngest(fc, db), hub, settings.Static{Concurrency: 4})

	// Subscribe before the sweep so both buffered events are captured.
	events, unsub := hub.Subscribe()
	defer unsub()

	if _, err := svc.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	// readEvent drains one event from the channel with a short timeout so a
	// missing event fails fast rather than hanging the test suite.
	readEvent := func(label string) sse.Event {
		t.Helper()
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("%s: event channel closed unexpectedly", label)
			}
			return ev
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: timed out waiting for SSE event", label)
			return sse.Event{} // unreachable; satisfies compiler
		}
	}

	// --- refresh.start ---
	startEv := readEvent("refresh.start")
	if startEv.Type != "refresh.start" {
		t.Errorf("first event type = %q, want %q", startEv.Type, "refresh.start")
	}
	var startPayload struct {
		Monitored int `json:"monitored"`
	}
	raw, ok := startEv.Data.(json.RawMessage)
	if !ok {
		t.Fatalf("refresh.start Data is %T, want json.RawMessage", startEv.Data)
	}
	if err := json.Unmarshal([]byte(raw), &startPayload); err != nil {
		t.Fatalf("unmarshal refresh.start payload: %v", err)
	}
	if startPayload.Monitored != 1 {
		t.Errorf("refresh.start monitored = %d, want 1", startPayload.Monitored)
	}

	// --- refresh.done ---
	doneEv := readEvent("refresh.done")
	if doneEv.Type != "refresh.done" {
		t.Errorf("second event type = %q, want %q", doneEv.Type, "refresh.done")
	}
}

func TestRefreshAll_PersistsSyncStateOnSuccess(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{
		42: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}},
	}}
	_, sp := seedMonitoredSeries(t, ctx, db, "echo", "mangadex", 42)

	// No sync-state row should exist before the sweep.
	if n := db.SuwayomiSyncState.Query().CountX(ctx); n != 0 {
		t.Fatalf("pre-sweep sync states = %d, want 0", n)
	}

	svc := newSvc(t, db, fc)
	if _, err := svc.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	st := db.SuwayomiSyncState.Query().
		Where(suwayomisyncstate.SeriesProviderID(sp.ID)).OnlyX(ctx)
	if st.LastSyncedAt == nil {
		t.Error("LastSyncedAt = nil, want set after a successful refresh")
	}
	if st.LastError != "" {
		t.Errorf("LastError = %q, want empty after success", st.LastError)
	}
}

func TestRefreshAll_PersistsSyncStateOnFailure(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{
		chaptersByManga: map[int][]suwayomi.Chapter{},
		failManga:       map[int]bool{42: true},
	}
	_, sp := seedMonitoredSeries(t, ctx, db, "echo", "mangadex", 42)

	svc := newSvc(t, db, fc)
	if _, err := svc.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	st := db.SuwayomiSyncState.Query().
		Where(suwayomisyncstate.SeriesProviderID(sp.ID)).OnlyX(ctx)
	if st.LastError == "" {
		t.Error("LastError = empty, want a recorded error after a failed refresh")
	}
	// A failed refresh must NOT stamp the success timestamp — last_synced_at marks
	// the last GOOD sync, so leaving it nil keeps the health calc honest.
	if st.LastSyncedAt != nil {
		t.Errorf("LastSyncedAt = %v, want nil after a failed refresh", st.LastSyncedAt)
	}
}

// TestRefreshAll_SkipsCompleted proves a completed series is excluded from the
// discovery sweep even while monitored, and returns to the sweep once it is
// un-completed (non-vacuous: the second half fails if the predicate is dropped).
func TestRefreshAll_SkipsCompleted(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := &fakeClient{chaptersByManga: map[int][]suwayomi.Chapter{42: {{ID: 1, Index: 0, Number: num(1), URL: "u1"}}}}
	s, _ := seedMonitoredSeries(t, ctx, db, "done", "mangadex", 42)

	// Mark completed → swept count is 0.
	db.Series.UpdateOneID(s.ID).SetCompleted(true).ExecX(ctx)
	svc := newSvc(t, db, fc)
	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.SeriesRefreshed != 0 {
		t.Fatalf("completed series swept: SeriesRefreshed = %d, want 0", res.SeriesRefreshed)
	}

	// Un-complete → swept count is 1.
	db.Series.UpdateOneID(s.ID).SetCompleted(false).ExecX(ctx)
	res2, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll (re-opened): %v", err)
	}
	if res2.SeriesRefreshed != 1 {
		t.Fatalf("re-opened series not swept: SeriesRefreshed = %d, want 1", res2.SeriesRefreshed)
	}
}
