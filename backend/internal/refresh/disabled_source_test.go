package refresh_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// stubDisabledSources is a refresh.DisabledSources that returns a fixed paused
// set, or a fixed error. It stands in for *disabledsource.Service so these tests
// need no second Postgres table — the sweep only ever asks it one question.
type stubDisabledSources struct {
	set map[int64]bool
	err error
}

// Disabled returns the stub's fixed answer.
func (s stubDisabledSources) Disabled(context.Context) (map[int64]bool, error) {
	return s.set, s.err
}

// newSvcWithPause builds a refresh.Service with the paused-source store attached,
// otherwise identical to newSvc (private uncached ingest, no gate).
func newSvcWithPause(t *testing.T, db *ent.Client, fc *enginefake.Client, d refresh.DisabledSources) *refresh.Service {
	t.Helper()
	return refresh.NewService(db, ingest.NewIngest(fc, db), sse.NewHub(),
		settings.Static{Concurrency: 4}, nil).
		WithDisabledSources(d)
}

// TestRefreshAll_SkipsPausedSource is the QCAT-513 refresh half: a paused
// source's provider is not re-fetched at all.
//
// The assertion is on the FETCH, not merely on the chapter count: the fake client
// records every Chapters call, so "the sweep did not touch the source" is proven
// directly rather than inferred from a side effect. The source is seeded with a
// chapter the sweep WOULD have discovered, which is what makes the test
// non-vacuous — without the skip it discovers one new chapter.
func TestRefreshAll_SkipsPausedSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 77, "/manga/paused"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
	}))
	seedMonitoredSeries(t, ctx, db, "paused", sourceID, mangaURL)

	svc := newSvcWithPause(t, db, fc, stubDisabledSources{set: map[int64]bool{sourceID: true}})
	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	// The series is still COUNTED as swept (it was considered) — only its paused
	// provider contributed nothing. A series whose every provider is paused is a
	// no-op, never an error.
	if res.SeriesRefreshed != 1 {
		t.Errorf("SeriesRefreshed = %d, want 1 — a paused provider must not hide its series", res.SeriesRefreshed)
	}
	if res.ProvidersRefreshed != 0 {
		t.Errorf("ProvidersRefreshed = %d, want 0 — a paused source is not re-fetched", res.ProvidersRefreshed)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0 — skipping a paused source is not a failure", res.Errors)
	}
	if res.NewChapters != 0 {
		t.Errorf("NewChapters = %d, want 0 — a paused source discovers nothing", res.NewChapters)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 0 {
		t.Errorf("chapter rows = %d, want 0 — the paused source's feed was never fetched", n)
	}
}

// TestRefreshAll_PausedSourceStillSweepsItsSiblings proves the skip is scoped to
// the paused SOURCE, not to the series carrying it: a series followed under two
// sources keeps discovering from the one that is still active. This is the
// behaviour QCAT-513 is actually for — the owner pauses a broken source and the
// alternative keeps the series current.
func TestRefreshAll_PausedSourceStillSweepsItsSiblings(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const (
		pausedID  int64 = 77
		activeID  int64 = 42
		pausedURL       = "/manga/dual-paused"
		activeURL       = "/manga/dual-active"
	)
	fc := enginefake.New(
		enginefake.WithChapters(pausedID, pausedURL, []sourceengine.Chapter{{Number: num(1), URL: "p1"}}),
		enginefake.WithChapters(activeID, activeURL, []sourceengine.Chapter{{Number: num(1), URL: "a1"}}),
	)
	s, _ := seedMonitoredSeries(t, ctx, db, "dual", pausedID, pausedURL)
	db.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(providerKey(activeID)).
		SetURL(activeURL).
		SetImportance(20).
		SaveX(ctx)

	svc := newSvcWithPause(t, db, fc, stubDisabledSources{set: map[int64]bool{pausedID: true}})
	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.ProvidersRefreshed != 1 {
		t.Errorf("ProvidersRefreshed = %d, want 1 — only the paused source is skipped", res.ProvidersRefreshed)
	}
	if res.NewChapters != 1 {
		t.Errorf("NewChapters = %d, want 1 — the active sibling still discovers", res.NewChapters)
	}
}

// TestRefreshAll_ResumingASourceRestoresDiscovery proves the skip is purely a
// read of the flag with no state left behind: the SAME series and the SAME
// provider row discover normally the moment the pause is lifted. Nothing had to
// be repaired, which is the Rule 2 claim (a pause deletes nothing) seen from the
// sweep's side.
func TestRefreshAll_ResumingASourceRestoresDiscovery(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 77, "/manga/resume"
	chapters := []sourceengine.Chapter{{Number: num(1), URL: "u1"}}
	seedMonitoredSeries(t, ctx, db, "resume", sourceID, mangaURL)

	paused := newSvcWithPause(t, db,
		enginefake.New(enginefake.WithChapters(sourceID, mangaURL, chapters)),
		stubDisabledSources{set: map[int64]bool{sourceID: true}})
	if _, err := paused.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll (paused): %v", err)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 0 {
		t.Fatalf("chapter rows while paused = %d, want 0", n)
	}

	// Resume: the store now reports nothing paused.
	resumed := newSvcWithPause(t, db,
		enginefake.New(enginefake.WithChapters(sourceID, mangaURL, chapters)),
		stubDisabledSources{set: map[int64]bool{}})
	res, err := resumed.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll (resumed): %v", err)
	}
	if res.NewChapters != 1 {
		t.Errorf("NewChapters after resuming = %d, want 1 — discovery restarts from the same rows", res.NewChapters)
	}
}

// TestRefreshAll_PauseStoreFailureStillSweeps pins the deliberate fail-open on a
// store READ error: discovery for every other source is worth more than a
// perfectly-honoured pause for one, and the paused source's own fetch failures
// are still bounded by the politeness gate. The failure is logged (see
// disabledSourceSet), never swallowed silently.
func TestRefreshAll_PauseStoreFailureStillSweeps(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 77, "/manga/store-broken"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
	}))
	seedMonitoredSeries(t, ctx, db, "store-broken", sourceID, mangaURL)

	svc := newSvcWithPause(t, db, fc, stubDisabledSources{err: errors.New("database is down")})
	res, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v — a pause-store read failure must not abort the sweep", err)
	}
	if res.NewChapters != 1 {
		t.Errorf("NewChapters = %d, want 1 — an unreadable pause store falls back to sweeping everything", res.NewChapters)
	}
}

// TestRefreshAll_NoPauseStoreIsUnchangedBehaviour pins the safe default: a
// service with no store attached (every existing call site, and every test that
// predates QCAT-513) sweeps exactly as it always did.
func TestRefreshAll_NoPauseStoreIsUnchangedBehaviour(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	const sourceID, mangaURL = 77, "/manga/no-store"
	fc := enginefake.New(enginefake.WithChapters(sourceID, mangaURL, []sourceengine.Chapter{
		{Number: num(1), URL: "u1"},
	}))
	seedMonitoredSeries(t, ctx, db, "no-store", sourceID, mangaURL)

	res, err := newSvc(t, db, fc).RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if res.NewChapters != 1 {
		t.Errorf("NewChapters = %d, want 1 — an unwired pause store means nothing is paused", res.NewChapters)
	}
}
