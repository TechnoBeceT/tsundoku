// Package refresh_test exercises the M5 discovery sweep against an ephemeral
// Postgres (testdb) and the shared sourceengine/fake.Client — no JVM, no
// network. This is the P2 (Suwayomi-removal) port of the original
// suwayomi.Client-backed suite onto the URL-addressed engine-host client.
package refresh_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// num is a small readability convenience for chapter-number literals;
// sourceengine.Chapter.Number itself is a plain (non-pointer) float64.
func num(n float64) float64 { return n }

// providerKey mirrors internal/ingest's private providerKey helper: the
// string form of a numeric engine-host source id, as stored in
// SeriesProvider.Provider. Kept local to the test package (black-box tests
// cannot reach the unexported helper).
func providerKey(sourceID int64) string {
	return strconv.FormatInt(sourceID, 10)
}

// newSvc builds a refresh.Service over the given db + fake client, with a
// PRIVATE, uncached ingest.Ingest (mirrors production: refresh never reads the
// interactive chapter cache — see refresh.NewService's doc comment).
func newSvc(t *testing.T, db *ent.Client, fc *enginefake.Client) *refresh.Service {
	t.Helper()
	ing := ingest.NewIngest(fc, db)
	return refresh.NewService(db, ing, sse.NewHub(), settings.Static{Concurrency: 4}, nil)
}

// seedMonitoredSeries creates a monitored series with one provider
// (provider = stringified sourceID, url = mangaURL), no chapters yet. Returns
// the series + series provider for later assertions.
func seedMonitoredSeries(t *testing.T, ctx context.Context, db *ent.Client, title string, sourceID int64, mangaURL string) (*ent.Series, *ent.SeriesProvider) {
	t.Helper()
	// Slug MUST equal disk.Slugify(title): AddSeries upserts the series by that
	// slug, so a mismatch would make refresh create a SECOND series and the
	// provider assertions below would read an empty row.
	s := db.Series.Create().SetTitle(title).SetSlug(disk.Slugify(title)).SetMonitored(true).SaveX(ctx)
	sp := db.SeriesProvider.Create().SetSeries(s).SetProvider(providerKey(sourceID)).SetURL(mangaURL).SetImportance(10).SaveX(ctx)
	return s, sp
}

func TestRefreshAll_DiscoversNewChapters(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 77, "/manga/alpha"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
	}))
	_, sp := seedMonitoredSeries(t, ctx, db, "alpha", sourceID, mangaURL)

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
	fc = enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
		{Number: num(2), URL: "u2"},
	}))
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

// TestRefreshAll_BypassesInteractiveChapterCache proves the sweep decoupling: even
// when refresh's ingest SHARES the interactive chapter cache (as it does in
// production wiring), the sweep fetches FRESH via FetchChaptersUncached and never
// reads the cache — so a stale cached list can never stale-out discovery. It
// pre-populates the cache with a 1-chapter STALE list for the manga, then runs a
// sweep whose client returns a FRESH 2-chapter list, and asserts both chapters
// were discovered (would be 1 if the cache were read) and a real client fetch ran.
func TestRefreshAll_BypassesInteractiveChapterCache(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		sourceID int64 = 77
		mangaURL       = "/manga/cached-series"
	)
	// Client (upstream truth) currently offers chapters 1 AND 2.
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
		{Number: num(2), URL: "u2"},
	}))
	seedMonitoredSeries(t, ctx, db, "cached-series", sourceID, mangaURL)

	// Pre-seed the SHARED interactive cache with a STALE 1-chapter list under the
	// exact key refresh would use if it read the cache.
	cache := ingest.NewChapterCacheConst(time.Hour)
	if _, err := cache.Get(ctx, sourceID, mangaURL, func() ([]sourceengine.Chapter, error) {
		return []sourceengine.Chapter{{Number: num(1), URL: "u1"}}, nil
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Build the sweep with a cache-bearing ingest (mirrors production wiring).
	ing := ingest.NewIngestWithGate(fc, db, cache, nil)
	svc := refresh.NewService(db, ing, sse.NewHub(), settings.Static{Concurrency: 4}, nil)

	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	// Both fresh chapters discovered ⇒ the cache's stale 1-chapter list was NOT read.
	if res.NewChapters != 2 {
		t.Fatalf("NewChapters = %d, want 2 (refresh must fetch fresh, not read the stale cache)", res.NewChapters)
	}
	// A real client fetch ran (a cache hit would leave this at 0).
	if got := fc.CallCount("Chapters"); got != 1 {
		t.Fatalf("Chapters called %d times, want 1 (fresh uncached fetch)", got)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 2 {
		t.Fatalf("chapter count = %d, want 2", n)
	}
}

func TestRefreshAll_SkipsUnmonitored(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 99, "/manga/beta"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}))

	s := db.Series.Create().SetTitle("beta").SetSlug("beta").SetMonitored(false).SaveX(ctx)
	db.SeriesProvider.Create().SetSeries(s).SetProvider(providerKey(sourceID)).SetURL(mangaURL).SaveX(ctx)

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

// TestRefreshAll_SkipsDiskOriginProvider proves a provider whose Provider
// column does NOT parse as a numeric source id (the disk-reconciler's
// display-name identity, e.g. "Other") is skipped — no fetch attempted, not
// counted as refreshed or errored — mirroring the old "unknown suwayomi_id"
// skip this replaces.
func TestRefreshAll_SkipsDiskOriginProvider(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := enginefake.New()

	s := db.Series.Create().SetTitle("gamma").SetSlug("gamma").SetMonitored(true).SaveX(ctx)
	// A disk-origin provider stores a display name, not a numeric id — must be
	// skipped, no fetch.
	db.SeriesProvider.Create().SetSeries(s).SetProvider("Other").SaveX(ctx)

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.ProvidersRefreshed != 0 || res.Errors != 0 {
		t.Errorf("res = %+v, want providers=0 errors=0 (disk-origin skipped, not failed)", res)
	}
}

// TestRefreshAll_SkipsUnknownURL proves a provider with a numeric source id but
// no stored URL (the DB backfill hasn't reached it yet) is skipped the same
// way — there is nothing to fetch.
func TestRefreshAll_SkipsUnknownURL(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fc := enginefake.New()

	s := db.Series.Create().SetTitle("delta-unknown").SetSlug("delta-unknown").SetMonitored(true).SaveX(ctx)
	db.SeriesProvider.Create().SetSeries(s).SetProvider(providerKey(55)).SaveX(ctx) // url left ""

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.ProvidersRefreshed != 0 || res.Errors != 0 {
		t.Errorf("res = %+v, want providers=0 errors=0 (unknown url skipped, not failed)", res)
	}
}

// TestRefreshAll_PartialFailureContinues proves one failing source-manga
// doesn't stop the sweep from refreshing the rest. fake.Client's WithError is
// global (every Chapters call fails), so this drives the per-provider partial
// failure via partialFailClient — a thin wrapper that fails ONLY the "bad"
// manga's fetch and delegates everything else to a real fake.Client.
func TestRefreshAll_PartialFailureContinues(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const (
		okSource, okURL   = 10, "/manga/ok"
		badSource, badURL = 20, "/manga/bad"
	)
	fc := enginefake.New(enginefake.WithChapters(okSource, okURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}))
	failing := &partialFailClient{Client: fc, failURL: badURL}

	seedMonitoredSeries(t, ctx, db, "ok", okSource, okURL)
	seedMonitoredSeries(t, ctx, db, "bad", badSource, badURL)

	ing := ingest.NewIngest(failing, db)
	svc := refresh.NewService(db, ing, sse.NewHub(), settings.Static{Concurrency: 4}, nil)

	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll must return nil error on partial failure, got %v", err)
	}
	if res.ProvidersRefreshed != 1 || res.Errors != 1 {
		t.Errorf("res = %+v, want providers=1 errors=1", res)
	}
}

// partialFailClient wraps a sourceengine.Client and forces Chapters to fail
// for exactly one url, succeeding (delegating) for every other call — used to
// drive the per-provider partial-failure path without failing every source in
// the sweep (unlike fake.Client's global WithError).
type partialFailClient struct {
	sourceengine.Client
	failURL string
}

func (p *partialFailClient) Chapters(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]sourceengine.Chapter, error) {
	if url == p.failURL {
		return nil, errors.New("source offline")
	}
	return p.Client.Chapters(ctx, sourceID, url, mangaTitle)
}

func TestRefreshAll_PreservesImportance_And_NeverDeletes(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 5, "/manga/delta"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
		{Number: num(2), URL: "u2"},
	}))
	_, sp := seedMonitoredSeries(t, ctx, db, "delta", sourceID, mangaURL) // importance 10

	if _, err := newSvc(t, db, fc).RefreshAll(ctx); err != nil {
		t.Fatalf("first RefreshAll: %v", err)
	}
	// Owner re-ranks: bump importance to 50.
	db.SeriesProvider.UpdateOne(sp).SetImportance(50).ExecX(ctx)

	// Source DROPS chapter 2 from its listing (never-delete must keep it).
	fc = enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
	}))
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
	const sourceID, mangaURL = 42, "/manga/echo"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}))
	seedMonitoredSeries(t, ctx, db, "echo", sourceID, mangaURL)

	hub := sse.NewHub()
	svc := refresh.NewService(db, ingest.NewIngest(fc, db), hub, settings.Static{Concurrency: 4}, nil)

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
	const sourceID, mangaURL = 42, "/manga/echo"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}))
	_, sp := seedMonitoredSeries(t, ctx, db, "echo", sourceID, mangaURL)

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
	const sourceID, mangaURL = 42, "/manga/echo"
	fc := enginefake.New(enginefake.WithError("Chapters", errors.New("source offline")))
	_, sp := seedMonitoredSeries(t, ctx, db, "echo", sourceID, mangaURL)

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
	const sourceID, mangaURL = 42, "/manga/done"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}))
	s, _ := seedMonitoredSeries(t, ctx, db, "done", sourceID, mangaURL)

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

// TestRefreshAll_SkipsGatedProvider proves a provider whose physical source is
// cooled down by the source-politeness gate (internal/sourcegate) is skipped
// entirely by the sweep — no fetch attempt, not counted as refreshed or
// errored — while a provider on an AVAILABLE source in the SAME sweep still
// refreshes normally.
func TestRefreshAll_SkipsGatedProvider(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const (
		blockedSource, blockedURL = 10, "/manga/blocked"
		okSource, okURL           = 20, "/manga/ok"
	)
	fc := enginefake.New(
		enginefake.WithChapters(blockedSource, blockedURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}),
		enginefake.WithChapters(okSource, okURL, []sourceengine.Chapter{{Number: num(1), URL: "u2"}}),
		enginefake.WithSources([]sourceengine.Source{
			{ID: blockedSource, Name: "BlockedSource"},
			{ID: okSource, Name: "OkSource"},
		}),
	)
	blockedSeries := createSeriesWithProvider(t, ctx, db, "blocked-series", blockedSource, blockedURL, "BlockedSource")
	createSeriesWithProvider(t, ctx, db, "ok-series", okSource, okURL, "OkSource")

	// Pre-trip the breaker for "BlockedSource" only.
	db.SourceCircuitState.Create().
		SetSourceKey("BlockedSource").
		SetConsecutiveFailures(5).
		SetCooldownUntil(time.Now().Add(time.Hour)).
		SaveX(ctx)

	gate := sourcegate.NewService(db, settings.Static{SourcesFailureThresh: 5, SourcesCooldownIv: time.Hour})
	svc := refresh.NewService(db, ingest.NewIngest(fc, db), sse.NewHub(), settings.Static{Concurrency: 4}, gate)

	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	// Only the available source's provider was refreshed; the gated one is
	// skipped entirely — not counted as an error, not attempted.
	if res.ProvidersRefreshed != 1 {
		t.Errorf("ProvidersRefreshed = %d, want 1 (only the available source)", res.ProvidersRefreshed)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0 (a gated-out provider is skipped, not a failure)", res.Errors)
	}
	// The blocked series' chapter was never ingested — the gate excluded it
	// from the sweep's work list entirely, so no fetch was even attempted.
	if n := db.Chapter.Query().Where(entchapter.HasSeriesWith(entseries.IDEQ(blockedSeries.ID))).CountX(ctx); n != 0 {
		t.Errorf("blocked series chapter count = %d, want 0 (never fetched)", n)
	}
}

// createSeriesWithProvider seeds a monitored series whose SINGLE provider also
// carries providerName — needed for TestRefreshAll_SkipsGatedProvider, whose
// gate key is the provider's DISPLAY name (see refresh.sourceKey), not the raw
// numeric provider id seedMonitoredSeries stores.
func createSeriesWithProvider(t *testing.T, ctx context.Context, db *ent.Client, title string, sourceID int64, mangaURL, providerName string) *ent.Series {
	t.Helper()
	s := db.Series.Create().SetTitle(title).SetSlug(disk.Slugify(title)).SetMonitored(true).SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(providerKey(sourceID)).
		SetProviderName(providerName).
		SetURL(mangaURL).
		SetImportance(10).
		SaveX(ctx)
	return s
}

// TestRefreshAll_GateAvailableRunsNormally proves that with NO breaker row at
// all (the common case), the gate never interferes with a normal sweep.
func TestRefreshAll_GateAvailableRunsNormally(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 42, "/manga/echo"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{{Number: num(1), URL: "u1"}}))
	seedMonitoredSeries(t, ctx, db, "echo", sourceID, mangaURL)

	gate := sourcegate.NewService(db, settings.Static{SourcesFailureThresh: 5, SourcesCooldownIv: time.Hour})
	svc := refresh.NewService(db, ingest.NewIngest(fc, db), sse.NewHub(), settings.Static{Concurrency: 4}, gate)

	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.ProvidersRefreshed != 1 || res.NewChapters != 1 {
		t.Errorf("res = %+v, want providers=1 new=1", res)
	}
}

// TestRefreshAll_DedupsScanlatorProviders proves Task A: a series followed under
// THREE scanlator-providers of the SAME (source, manga) triggers exactly ONE
// upstream Chapters call in a sweep, and every provider still ingests only its
// own scanlator's chapters from that single fetch.
func TestRefreshAll_DedupsScanlatorProviders(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 77, "/manga/multi"
	// One manga whose feed carries three scanlators; one Chapters call
	// returns ALL of them.
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1", Scanlator: "Alpha"},
		{Number: num(2), URL: "u2", Scanlator: "Alpha"},
		{Number: num(3), URL: "u3", Scanlator: "Beta"},
		{Number: num(4), URL: "u4", Scanlator: "Gamma"},
	}))

	s := db.Series.Create().SetTitle("multi").SetSlug(disk.Slugify("multi")).SetMonitored(true).SaveX(ctx)
	spA := db.SeriesProvider.Create().SetSeries(s).SetProvider(providerKey(sourceID)).SetURL(mangaURL).SetScanlator("Alpha").SetImportance(30).SaveX(ctx)
	spB := db.SeriesProvider.Create().SetSeries(s).SetProvider(providerKey(sourceID)).SetURL(mangaURL).SetScanlator("Beta").SetImportance(20).SaveX(ctx)
	spC := db.SeriesProvider.Create().SetSeries(s).SetProvider(providerKey(sourceID)).SetURL(mangaURL).SetScanlator("Gamma").SetImportance(10).SaveX(ctx)

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if got := fc.CallCount("Chapters"); got != 1 {
		t.Fatalf("Chapters called %d times, want 1 (per-sweep dedup)", got)
	}
	if res.ProvidersRefreshed != 3 || res.NewChapters != 4 {
		t.Errorf("res = %+v, want providers=3 new=4", res)
	}
	// Each provider ingested exactly its scanlator's chapters from the shared fetch.
	assertProviderChapterCount(t, ctx, db, spA.ID, 2)
	assertProviderChapterCount(t, ctx, db, spB.ID, 1)
	assertProviderChapterCount(t, ctx, db, spC.ID, 1)
}

// assertProviderChapterCount fails unless spID has exactly want ProviderChapter rows.
func assertProviderChapterCount(t *testing.T, ctx context.Context, db *ent.Client, spID uuid.UUID, want int) {
	t.Helper()
	got := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(spID)).CountX(ctx)
	if got != want {
		t.Errorf("provider %s has %d ProviderChapters, want %d", spID, got, want)
	}
}
