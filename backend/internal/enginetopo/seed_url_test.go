// Package enginetopo_test exercises the enginetopo one-shot maintenance passes
// (SeriesProvider.url backfill, source-preference seeding, engine-config
// seeding) against an ephemeral Postgres (testdb) and an in-process fake
// suwayomi.Client — no JVM, no network. The fakeClient type defined in this
// file is shared by every *_test.go in the package (§2 DRY).
package enginetopo_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fakeClient implements suwayomi.Client. Only MangaMeta is exercised by the
// backfill; every other method satisfies the interface (the same
// stub-everything-else pattern used by internal/refresh's fakeClient — see
// its doc comment on the INTERFACE FOOTGUN). urls maps a Suwayomi manga id to
// the URL MangaMeta should return; errs maps a manga id to an error MangaMeta
// should return instead (mutually exclusive per id in these tests).
type fakeClient struct {
	urls map[int]string
	errs map[int]error

	mu    sync.Mutex
	calls map[int]int

	// prefsBySource / prefsErrBySource configure SourcePreferences per
	// sourceID (used by seed_prefs_test.go); prefsCalls counts invocations.
	prefsBySource    map[string][]suwayomi.SourcePreference
	prefsErrBySource map[string]error
	prefsCalls       map[string]int

	// serverSettings / serverSettingsErr configure ServerSettings (used by
	// seed_config_test.go).
	serverSettings    suwayomi.SuwayomiSettings
	serverSettingsErr error
}

func (f *fakeClient) MangaMeta(_ context.Context, mangaID int) (suwayomi.Manga, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = make(map[int]int)
	}
	f.calls[mangaID]++
	f.mu.Unlock()
	if err, ok := f.errs[mangaID]; ok {
		return suwayomi.Manga{}, err
	}
	return suwayomi.Manga{URL: f.urls[mangaID]}, nil
}

func (f *fakeClient) callCount(mangaID int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[mangaID]
}

// prefsCallCount reports how many times SourcePreferences was called for
// sourceID (used by seed_prefs_test.go to assert per-source skip behavior).
func (f *fakeClient) prefsCallCount(sourceID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prefsCalls[sourceID]
}

func (f *fakeClient) Sources(context.Context) ([]suwayomi.Source, error) { return nil, nil }
func (f *fakeClient) Search(context.Context, string, string) ([]suwayomi.Manga, error) {
	return nil, nil
}
func (f *fakeClient) Browse(context.Context, string, suwayomi.BrowseType, int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (f *fakeClient) FetchChapters(context.Context, int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) MangaChapters(context.Context, int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) FetchMangaDetails(context.Context, int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeClient) ChapterPages(context.Context, int) ([]string, error)       { return nil, nil }
func (f *fakeClient) PageBytes(context.Context, string) ([]byte, string, error) { return nil, "", nil }
func (f *fakeClient) ServerSettings(context.Context) (suwayomi.SuwayomiSettings, error) {
	if f.serverSettingsErr != nil {
		return suwayomi.SuwayomiSettings{}, f.serverSettingsErr
	}
	return f.serverSettings, nil
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
func (f *fakeClient) SourcePreferences(_ context.Context, sourceID string) ([]suwayomi.SourcePreference, error) {
	f.mu.Lock()
	if f.prefsCalls == nil {
		f.prefsCalls = make(map[string]int)
	}
	f.prefsCalls[sourceID]++
	f.mu.Unlock()
	if err, ok := f.prefsErrBySource[sourceID]; ok {
		return nil, err
	}
	return f.prefsBySource[sourceID], nil
}
func (f *fakeClient) SetSourcePreference(context.Context, string, int, suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeClient) ExtensionSources(context.Context, string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (f *fakeClient) SetSourceEnabled(context.Context, string, bool) error { return nil }

// seedProvider creates a Series + one SeriesProvider row (url="" unless
// overridden) with the given suwayomi_id, mirroring a real ingested row
// belonging to an unmonitored/completed series that the refresh sweep never
// touched. monitored/completed are irrelevant to the backfill (it is
// deliberately UNGATED), so the seed does not bother setting them.
func seedProvider(ctx context.Context, t *testing.T, client *ent.Client, title, provider string, suwayomiID int) *ent.SeriesProvider {
	t.Helper()
	s := client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SaveX(ctx)
	return client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(provider).
		SetSuwayomiID(suwayomiID).
		SaveX(ctx)
}

// TestBackfillProviderURLs_FillsEveryEmptyRow proves the happy path: three
// rows with url="" and a resolvable MangaMeta URL are all filled in one pass.
func TestBackfillProviderURLs_FillsEveryEmptyRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sp1 := seedProvider(ctx, t, client, "Solo Leveling", "mangadex", 101)
	sp2 := seedProvider(ctx, t, client, "Omniscient Reader", "comix", 102)
	sp3 := seedProvider(ctx, t, client, "The Beginning After The End", "webtoon", 103)

	fc := &fakeClient{urls: map[int]string{
		101: "https://mangadex.test/manga/solo-leveling",
		102: "https://comix.test/manga/omniscient-reader",
		103: "https://webtoon.test/manga/tbate",
	}}

	filled, remaining, err := enginetopo.BackfillProviderURLs(ctx, fc, client)
	if err != nil {
		t.Fatalf("BackfillProviderURLs: %v", err)
	}
	if filled != 3 {
		t.Errorf("filled = %d, want 3", filled)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}

	for _, tc := range []struct {
		sp   *ent.SeriesProvider
		want string
	}{
		{sp1, "https://mangadex.test/manga/solo-leveling"},
		{sp2, "https://comix.test/manga/omniscient-reader"},
		{sp3, "https://webtoon.test/manga/tbate"},
	} {
		got := client.SeriesProvider.GetX(ctx, tc.sp.ID)
		if got.URL != tc.want {
			t.Errorf("SeriesProvider %s URL = %q, want %q", tc.sp.ID, got.URL, tc.want)
		}
	}
}

// TestBackfillProviderURLs_PerRowFailureSkipsButContinues proves partial
// success: a MangaMeta failure on ONE row leaves it in `remaining` (and
// url="") without aborting or panicking on the other rows.
func TestBackfillProviderURLs_PerRowFailureSkipsButContinues(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sp1 := seedProvider(ctx, t, client, "Solo Leveling", "mangadex", 201)
	sp2 := seedProvider(ctx, t, client, "Omniscient Reader", "comix", 202)
	sp3 := seedProvider(ctx, t, client, "The Beginning After The End", "webtoon", 203)

	fc := &fakeClient{
		urls: map[int]string{
			201: "https://mangadex.test/manga/solo-leveling",
			203: "https://webtoon.test/manga/tbate",
		},
		errs: map[int]error{202: errors.New("source offline")},
	}

	filled, remaining, err := enginetopo.BackfillProviderURLs(ctx, fc, client)
	if err != nil {
		t.Fatalf("BackfillProviderURLs: %v", err)
	}
	if filled != 2 {
		t.Errorf("filled = %d, want 2", filled)
	}
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1", remaining)
	}

	if got := client.SeriesProvider.GetX(ctx, sp1.ID); got.URL == "" {
		t.Error("sp1 URL still empty, want filled")
	}
	if got := client.SeriesProvider.GetX(ctx, sp2.ID); got.URL != "" {
		t.Errorf("sp2 URL = %q, want still empty (MangaMeta failed)", got.URL)
	}
	if got := client.SeriesProvider.GetX(ctx, sp3.ID); got.URL == "" {
		t.Error("sp3 URL still empty, want filled")
	}
}

// TestBackfillProviderURLs_IdempotentSecondRun proves a second pass over a
// fully-populated library is a true no-op: filled=0, no MangaMeta calls made
// (the WHERE url="" clause excludes every already-filled row).
func TestBackfillProviderURLs_IdempotentSecondRun(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "mangadex", 301)
	seedProvider(ctx, t, client, "Omniscient Reader", "comix", 302)

	fc := &fakeClient{urls: map[int]string{
		301: "https://mangadex.test/manga/solo-leveling",
		302: "https://comix.test/manga/omniscient-reader",
	}}

	filled, remaining, err := enginetopo.BackfillProviderURLs(ctx, fc, client)
	if err != nil {
		t.Fatalf("first BackfillProviderURLs: %v", err)
	}
	if filled != 2 || remaining != 0 {
		t.Fatalf("first pass filled=%d remaining=%d, want 2/0", filled, remaining)
	}

	filled2, remaining2, err := enginetopo.BackfillProviderURLs(ctx, fc, client)
	if err != nil {
		t.Fatalf("second BackfillProviderURLs: %v", err)
	}
	if filled2 != 0 {
		t.Errorf("second pass filled = %d, want 0", filled2)
	}
	if remaining2 != 0 {
		t.Errorf("second pass remaining = %d, want 0", remaining2)
	}
	if c := fc.callCount(301); c != 1 {
		t.Errorf("MangaMeta(301) called %d times on the second pass, want 0 more (1 total)", c)
	}
	if c := fc.callCount(302); c != 1 {
		t.Errorf("MangaMeta(302) called %d times on the second pass, want 0 more (1 total)", c)
	}
}
