package download_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// pausedSource is the engine source id these tests pause, stored as the numeric
// string a LIVE-ingested provider carries in SeriesProvider.provider.
const (
	pausedSourceID  int64 = 599
	pausedSourceStr       = "599"
	activeSourceStr       = "42"
)

// recordingFetcher wraps the deterministic fake and records the Provider of every
// FetchRef it is handed. It is what makes these tests assert on the ENGINE'S
// BEHAVIOUR — which source was actually contacted — rather than on a state field
// that several unrelated code paths could have produced.
type recordingFetcher struct {
	inner fetcher.ChapterFetcher
	mu    sync.Mutex
	seen  []string
}

// newRecordingFetcher wraps a plain successful fake.
func newRecordingFetcher() *recordingFetcher {
	return &recordingFetcher{inner: fake.New()}
}

// Fetch records ref.Provider and delegates to the wrapped fake.
func (r *recordingFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	r.mu.Lock()
	r.seen = append(r.seen, ref.Provider)
	r.mu.Unlock()
	return r.inner.Fetch(ctx, ref)
}

// providers returns the recorded provider list.
func (r *recordingFetcher) providers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

// stubDisabledSources is a download.DisabledSources returning a fixed paused set.
// It stands in for *disabledsource.Service — the dispatcher only ever asks it one
// question, so a real table would add a dependency without adding coverage.
type stubDisabledSources struct {
	set map[int64]bool
}

// Disabled returns the stub's fixed paused set.
func (s stubDisabledSources) Disabled(context.Context) (map[int64]bool, error) {
	return s.set, nil
}

// seedTwoSourceChapter creates a series with a wanted chapter offered by TWO live
// sources: the higher-importance one is the source these tests pause, so the
// dispatcher must fall through to the lower-importance one. Returns the chapter.
func seedTwoSourceChapter(ctx context.Context, t *testing.T, client *ent.Client, title, slug string) *ent.Chapter {
	t.Helper()
	s := client.Series.Create().SetTitle(title).SetSlug(slug).SaveX(ctx)
	for _, src := range []struct {
		provider   string
		name       string
		importance int
	}{
		{pausedSourceStr, "Comix", 30},
		{activeSourceStr, "Hive Scans", 20},
	} {
		sp := client.SeriesProvider.Create().
			SetSeries(s).
			SetProvider(src.provider).
			SetProviderName(src.name).
			SetImportance(src.importance).
			SaveX(ctx)
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey("ch-1").
			SetURL("https://" + src.name + ".example.com/ch-1").
			SetProviderIndex(0).
			SaveX(ctx)
	}
	return client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)
}

// TestRunOnce_PausedSourceIsNeverFetched is the end-to-end QCAT-513 download
// claim, and it is what pins the WIRING rather than the ranking core: without
// WithDisabledSources on the dispatcher the drop in internal/chapter is
// unreachable from production, and the chapter-level tests would still be green.
//
// The paused source is the HIGHEST-importance one, so with no pause it wins every
// time — the test cannot pass by accident.
func TestRunOnce_PausedSourceIsNeverFetched(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch := seedTwoSourceChapter(ctx, t, client, "Paused Download", "paused-download")

	f := newRecordingFetcher()
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil).
		WithDisabledSources(stubDisabledSources{set: map[int64]bool{pausedSourceID: true}})

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state = %q, want downloaded — the active source must still serve the chapter", got.State)
	}
	seen := f.providers()
	if len(seen) != 1 || seen[0] != activeSourceStr {
		t.Fatalf("fetched providers = %v, want exactly [%s] — the paused source must never be contacted",
			seen, activeSourceStr)
	}
	// And the chapter is genuinely satisfied by the source that served it.
	sp := client.SeriesProvider.GetX(ctx, *got.SatisfiedByProviderID)
	if sp.Provider != activeSourceStr {
		t.Errorf("satisfied by %q, want %q — the pause must redirect the chapter, not just skip it",
			sp.Provider, activeSourceStr)
	}
}

// TestRunOnce_ChapterWithOnlyAPausedSourceStaysWanted covers the "goes dark"
// series: the chapter's ONLY source is paused, so nothing can fetch it.
//
// It must stay WANTED — not permanently_failed. A pause is temporary and spends
// no retry budget, so marking the chapter terminal would mean the owner had to
// hunt down and retry every affected chapter after resuming the source. Nothing
// is fetched and nothing is deleted.
func TestRunOnce_ChapterWithOnlyAPausedSourceStaysWanted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Dark Series").SetSlug("dark-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(pausedSourceStr).
		SetProviderName("Comix").
		SetImportance(30).
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-1").
		SetURL("https://comix.example.com/ch-1").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)

	f := newRecordingFetcher()
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil).
		WithDisabledSources(stubDisabledSources{set: map[int64]bool{pausedSourceID: true}})

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateWanted {
		t.Errorf("state = %q, want wanted — a paused source spends no budget, so the chapter is not terminal", got.State)
	}
	if seen := f.providers(); len(seen) != 0 {
		t.Errorf("fetched providers = %v, want none", seen)
	}
	// Nothing was charged against the source: resuming it must find a clean budget.
	pc := client.ProviderChapter.Query().OnlyX(ctx)
	if pc.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a pause is not a failed attempt", pc.Attempts)
	}
	if pc.LastError != "" {
		t.Errorf("last_error = %q, want empty — a pause is not an error", pc.LastError)
	}
}

// TestRunOnce_NoPauseStoreIsUnchangedBehaviour pins the safe default every
// existing construction of download.New relies on: with no store attached
// nothing is paused, and the highest-importance source wins as always.
func TestRunOnce_NoPauseStoreIsUnchangedBehaviour(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedTwoSourceChapter(ctx, t, client, "Unpaused Download", "unpaused-download")

	f := newRecordingFetcher()
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	seen := f.providers()
	if len(seen) != 1 || seen[0] != pausedSourceStr {
		t.Fatalf("fetched providers = %v, want exactly [%s] — with no pause the best source wins",
			seen, pausedSourceStr)
	}
}

// TestDetectUpgrades_PausedSourceIsNeverAnUpgradeTarget covers the OTHER half of
// the dispatcher: convergence upgrades. A chapter satisfied by a low-importance
// source, with a higher-importance PAUSED source also offering it, must NOT be
// flagged upgrade_available — otherwise the library would sit permanently flagged
// for an upgrade that can never run, re-evaluated every single cycle.
func TestDetectUpgrades_PausedSourceIsNeverAnUpgradeTarget(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Upgrade Paused").SetSlug("upgrade-paused").SaveX(ctx)

	low := client.SeriesProvider.Create().
		SetSeries(s).SetProvider(activeSourceStr).SetProviderName("Hive Scans").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(low.ID).SetChapterKey("ch-1").
		SetURL("https://hive.example.com/ch-1").SetProviderIndex(0).SaveX(ctx)

	high := client.SeriesProvider.Create().
		SetSeries(s).SetProvider(pausedSourceStr).SetProviderName("Comix").SetImportance(30).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(high.ID).SetChapterKey("ch-1").
		SetURL("https://comix.example.com/ch-1").SetProviderIndex(0).SaveX(ctx)

	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("ch-1").
		SetState(entchapter.StateDownloaded).
		SetFilename("[Hive Scans][en] Upgrade Paused 001.cbz").
		SetSatisfiedByID(low.ID).
		SetSatisfiedImportance(low.Importance).
		SaveX(ctx)

	d := download.New(client, newRecordingFetcher(), sse.NewHub(),
		download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil).
		WithDisabledSources(stubDisabledSources{set: map[int64]bool{pausedSourceID: true}})

	flagged, err := d.DetectUpgrades(ctx, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if flagged != 0 {
		t.Errorf("flagged = %d, want 0 — a paused source must never be an upgrade target", flagged)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("state = %q, want downloaded — the chapter must stay settled while its better source is paused", got.State)
	}
	if got.Filename == "" {
		t.Error("filename was cleared — a pause must never take a chapter's file away")
	}
}

// TestDetectUpgrades_ResumingASourceFlagsTheUpgrade is the counterpart proving
// the previous test is not vacuous: the SAME library, with the pause lifted,
// flags exactly the upgrade the pause suppressed.
func TestDetectUpgrades_ResumingASourceFlagsTheUpgrade(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Upgrade Resumed").SetSlug("upgrade-resumed").SaveX(ctx)

	low := client.SeriesProvider.Create().
		SetSeries(s).SetProvider(activeSourceStr).SetProviderName("Hive Scans").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(low.ID).SetChapterKey("ch-1").
		SetURL("https://hive.example.com/ch-1").SetProviderIndex(0).SaveX(ctx)

	high := client.SeriesProvider.Create().
		SetSeries(s).SetProvider(pausedSourceStr).SetProviderName("Comix").SetImportance(30).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(high.ID).SetChapterKey("ch-1").
		SetURL("https://comix.example.com/ch-1").SetProviderIndex(0).SaveX(ctx)

	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("ch-1").
		SetState(entchapter.StateDownloaded).
		SetFilename("[Hive Scans][en] Upgrade Resumed 001.cbz").
		SetSatisfiedByID(low.ID).
		SetSatisfiedImportance(low.Importance).
		SaveX(ctx)

	d := download.New(client, newRecordingFetcher(), sse.NewHub(),
		download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil).
		WithDisabledSources(stubDisabledSources{set: map[int64]bool{}})

	flagged, err := d.DetectUpgrades(ctx, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if flagged != 1 {
		t.Fatalf("flagged = %d, want 1 — with nothing paused the better source is a real upgrade target", flagged)
	}
	if got := client.Chapter.GetX(ctx, ch.ID); got.State != entchapter.StateUpgradeAvailable {
		t.Errorf("state = %q, want upgrade_available", got.State)
	}
}
