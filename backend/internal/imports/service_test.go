// Package imports_test — unit tests for Service (Sources, Search, InspectChapters, Adopt).
//
// Task 3 tests use an in-process fakeClient; no engine-host process, no network, no DB.
// Task 4 Adopt tests additionally require testdb (ephemeral Postgres via Docker).
package imports_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// --- fake client -------------------------------------------------------------

// fakeClient implements sourceengine.Client with canned per-source responses.
// Methods unused by Service return nil, nil.
//
// For Adopt tests (Task 4) the client dispatches Chapters by manga URL:
//   - chaptersByURL maps url → chapters to return.
//   - chapterErrsByURL maps url → error to return (takes priority).
//
// The original flat chapters/chaptersErr fields remain for Task 3 compatibility;
// if chaptersByURL is non-nil it takes priority over the flat fields.
type fakeClient struct {
	// sources is the slice returned by Sources.
	sources []sourceengine.Source
	// sourcesErr is the error returned by Sources (nil = success).
	sourcesErr error
	// searchResults maps sourceID → results returned by Search.
	searchResults map[int64]sourceengine.SearchResult
	// searchErrs maps sourceID → error returned by Search (nil = success).
	searchErrs map[int64]error
	// popularResults maps sourceID → result returned by Popular.
	popularResults map[int64]sourceengine.SearchResult
	// latestResults maps sourceID → result returned by Latest.
	latestResults map[int64]sourceengine.SearchResult
	// browseErr is the error returned by Popular/Latest (nil = success).
	browseErr error
	// chapters is the slice returned by Chapters (Task 3 flat path).
	chapters []sourceengine.Chapter
	// chaptersErr is the error returned by Chapters (Task 3 flat path).
	chaptersErr error
	// chaptersByURL maps manga url → chapters (Task 4 per-manga path).
	// Non-nil activates the per-manga dispatch.
	chaptersByURL map[string][]sourceengine.Chapter
	// chapterErrsByURL maps manga url → error (Task 4 per-manga error injection).
	chapterErrsByURL map[string]error
	// detailsByURL maps manga url → the MangaDetails returned by MangaDetails.
	detailsByURL map[string]sourceengine.MangaDetails
	// detailsErr is the error returned by MangaDetails (nil = success).
	detailsErr error
}

// Compile-time assertion: fakeClient must satisfy sourceengine.Client.
var _ sourceengine.Client = (*fakeClient)(nil)

func (f *fakeClient) Health(_ context.Context) (sourceengine.Health, error) {
	return sourceengine.Health{}, nil
}

func (f *fakeClient) Sources(_ context.Context) ([]sourceengine.Source, error) {
	return f.sources, f.sourcesErr
}

func (f *fakeClient) Search(_ context.Context, sourceID int64, _ string, _ int) (sourceengine.SearchResult, error) {
	if f.searchErrs != nil {
		if err, ok := f.searchErrs[sourceID]; ok {
			return sourceengine.SearchResult{}, err
		}
	}
	if f.searchResults != nil {
		if res, ok := f.searchResults[sourceID]; ok {
			return res, nil
		}
	}
	return sourceengine.SearchResult{}, nil
}

func (f *fakeClient) Popular(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	if f.browseErr != nil {
		return sourceengine.SearchResult{}, f.browseErr
	}
	if f.popularResults != nil {
		return f.popularResults[sourceID], nil
	}
	return sourceengine.SearchResult{}, nil
}

func (f *fakeClient) Latest(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	if f.browseErr != nil {
		return sourceengine.SearchResult{}, f.browseErr
	}
	if f.latestResults != nil {
		return f.latestResults[sourceID], nil
	}
	return sourceengine.SearchResult{}, nil
}

func (f *fakeClient) MangaDetails(_ context.Context, _ int64, url string) (sourceengine.MangaDetails, error) {
	if f.detailsErr != nil {
		return sourceengine.MangaDetails{}, f.detailsErr
	}
	if f.detailsByURL != nil {
		return f.detailsByURL[url], nil
	}
	return sourceengine.MangaDetails{}, nil
}

func (f *fakeClient) Chapters(_ context.Context, _ int64, url string, _ string) ([]sourceengine.Chapter, error) {
	// Per-manga dispatch (Task 4): error first, then chapters.
	if f.chapterErrsByURL != nil {
		if err, ok := f.chapterErrsByURL[url]; ok {
			return nil, err
		}
	}
	if f.chaptersByURL != nil {
		return f.chaptersByURL[url], nil
	}
	// Flat fallback (Task 3).
	return f.chapters, f.chaptersErr
}

// Remaining Client methods are unused by Service; return nil, nil.
func (f *fakeClient) Pages(_ context.Context, _ int64, _ string) ([]sourceengine.Page, error) {
	return nil, nil
}
func (f *fakeClient) Image(_ context.Context, _ int64, _, _ string) ([]byte, string, error) {
	return nil, "", nil
}
func (f *fakeClient) Preferences(_ context.Context, _ int64) ([]sourceengine.Preference, error) {
	return nil, nil
}
func (f *fakeClient) SetPreferences(_ context.Context, _ int64, _ map[string]any) ([]sourceengine.Preference, error) {
	return nil, nil
}
func (f *fakeClient) Extensions(_ context.Context) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeClient) InstallExtension(_ context.Context, _, _ string) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeClient) RefreshExtensions(_ context.Context) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeClient) UpdateExtension(_ context.Context, _ string) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeClient) UninstallExtension(_ context.Context, _ string) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeClient) Repos(_ context.Context) ([]string, error) { return nil, nil }
func (f *fakeClient) SetRepos(_ context.Context, _ []string) ([]string, error) {
	return nil, nil
}
func (f *fakeClient) SetFlareSolverr(_ context.Context, _ sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	return sourceengine.FlareSolverrConfig{}, nil
}
func (f *fakeClient) SetSocks(_ context.Context, _ sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	return sourceengine.SocksConfig{}, nil
}

// --- helpers -----------------------------------------------------------------

// testSearchTimeout is a generous overall-search deadline used by tests that are
// not exercising the deadline behaviour itself — long enough never to fire for
// an in-memory fake client. The dedicated partial-results test passes its own
// short value directly.
const testSearchTimeout = 30 * time.Second

// newService constructs a Service with a fake client and nil ingest/db (unused in Task 3).
func newService(fc *fakeClient) *imports.Service {
	return imports.NewService(fc, nil, nil, "", testSearchTimeout, nil)
}

// makeAdoptChapters builds n stub sourceengine.Chapter values, numbered 1..n,
// with URLs derived from urlPrefix so that distinct providers in the same test
// get non-overlapping per-chapter URLs. Each chapter's sequential number gives
// NormalizeChapterKey distinct, deterministic keys.
func makeAdoptChapters(urlPrefix string, n int) []sourceengine.Chapter {
	chs := make([]sourceengine.Chapter, n)
	for i := range n {
		num := float64(i + 1)
		chs[i] = sourceengine.Chapter{
			Name:   fmt.Sprintf("Chapter %.0f", num),
			Number: num,
			URL:    fmt.Sprintf("%s/ch/%d", urlPrefix, i+1),
		}
	}
	return chs
}

// --- metrics recording (batch-after-fan-out) --------------------------------

// captureRecorder is a metrics.Recorder test double that captures the batch(es)
// it is handed, so a test can assert Search records one timing per source that
// ran (success AND failure), in ONE batch after the fan-out.
type captureRecorder struct {
	mu      sync.Mutex
	batches [][]metrics.Sample
}

func (r *captureRecorder) Record(_ context.Context, sourceID, sourceName string, latency time.Duration, sourceErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, []metrics.Sample{{SourceID: sourceID, SourceName: sourceName, Latency: latency, Err: sourceErr}})
}
func (r *captureRecorder) RecordBatch(_ context.Context, samples []metrics.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, samples)
}

// waitForBatches polls until at least n batches have been captured — recording is
// asynchronous (a background goroutine in Search) and best-effort — or the short
// deadline elapses, then returns a snapshot copy safe to read without the lock.
func (r *captureRecorder) waitForBatches(t *testing.T, n int) [][]metrics.Sample {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.batches)
		snap := append([][]metrics.Sample(nil), r.batches...)
		r.mu.Unlock()
		if got >= n {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d metrics batch(es); got %d", n, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSearch_RecordsMetricsBatch proves Search records exactly ONE batch after
// the fan-out with one sample per source that ran — the failing source included
// (with its Err set), which is the datapoint that flags a source slow.
func TestSearch_RecordsMetricsBatch(t *testing.T) {
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
	rec := &captureRecorder{}
	svc := imports.NewService(fc, nil, nil, "", testSearchTimeout, rec)

	if _, err := svc.Search(context.Background(), "q", nil); err != nil {
		t.Fatalf("Search: %v", err)
	}

	batches := rec.waitForBatches(t, 1)
	if len(batches) != 1 {
		t.Fatalf("recorded %d batches, want exactly 1 (batch-after-fan-out)", len(batches))
	}
	byID := map[string]metrics.Sample{}
	for _, s := range batches[0] {
		byID[s.SourceID] = s
	}
	if len(byID) != 2 {
		t.Fatalf("batch has %d distinct sources, want 2", len(byID))
	}
	if byID["1"].Err != nil {
		t.Errorf("source 1 sample Err = %v, want nil", byID["1"].Err)
	}
	if !errors.Is(byID["2"].Err, boom) {
		t.Errorf("source 2 sample Err = %v, want the source failure", byID["2"].Err)
	}
}

// TestSearch_RanksBestMatchFirst proves the regression fix end-to-end: given a
// query whose exact match is buried among many decoy results across several
// sources (the engine returns far more, source-unranked hits than the old
// Suwayomi client), Search returns the exact-match group FIRST regardless of the
// arbitrary goroutine-completion order the fan-out appends candidates in.
func TestSearch_RanksBestMatchFirst(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Alpha", Lang: "en"},
			{ID: 2, Name: "Beta", Lang: "en"},
			{ID: 3, Name: "Gamma", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			// Decoy-heavy source: many loose fuzzy hits, no exact/substring match.
			1: {Manga: []sourceengine.MangaEntry{
				{Title: "Berserk", URL: "/b"},
				{Title: "Omniscient Reader", URL: "/o"},
				{Title: "Tower of God", URL: "/t"},
			}},
			// A "contains the query" hit (must rank below exact, above fuzzy).
			2: {Manga: []sourceengine.MangaEntry{
				{Title: "Solo Leveling: Ragnarok", URL: "/slr"},
				{Title: "Vinland Saga", URL: "/v"},
			}},
			// The exact match — on the LAST source, so insertion order alone would
			// never put it first.
			3: {Manga: []sourceengine.MangaEntry{
				{Title: "Nano Machine", URL: "/n"},
				{Title: "Solo Leveling", URL: "/sl"},
			}},
		},
	}
	svc := newService(fc)

	groups, err := svc.Search(context.Background(), "Solo Leveling", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(groups) < 2 {
		t.Fatalf("want several groups; got %d", len(groups))
	}
	if groups[0].Title != "Solo Leveling" {
		titles := make([]string, len(groups))
		for i, g := range groups {
			titles[i] = g.Title
		}
		t.Fatalf("group[0].Title = %q, want exact match %q (full order: %v)", groups[0].Title, "Solo Leveling", titles)
	}
	// The "contains" match must outrank the loose fuzzy decoys (rank second).
	if groups[1].Title != "Solo Leveling: Ragnarok" {
		t.Errorf("group[1].Title = %q, want the contains-match %q", groups[1].Title, "Solo Leveling: Ragnarok")
	}
}

// --- Sources tests -----------------------------------------------------------

// TestService_Sources verifies that Sources maps the client list to []SourceDTO.
func TestService_Sources(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Alpha Source", Lang: "en"},
			{ID: 2, Name: "Beta Source", Lang: "ko"},
		},
	}
	svc := newService(fc)

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Sources: got %d DTOs, want 2", len(got))
	}
	if got[0].ID != "1" || got[0].Name != "Alpha Source" || got[0].Lang != "en" {
		t.Errorf("Sources[0]: got %+v, want {1 Alpha Source en}", got[0])
	}
	if got[1].ID != "2" || got[1].Name != "Beta Source" || got[1].Lang != "ko" {
		t.Errorf("Sources[1]: got %+v, want {2 Beta Source ko}", got[1])
	}
}

// TestService_Sources_Error verifies that a client error is propagated.
func TestService_Sources_Error(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("client: no sources")
	fc := &fakeClient{sourcesErr: sentinel}
	svc := newService(fc)

	_, err := svc.Sources(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("Sources: err got %v, want to wrap %v", err, sentinel)
	}
}

// TestService_Sources_ExcludesInfinityScans verifies that InfinityScans — a
// known-broken source whose captcha is broken (hitting it wastes requests and
// risks IP-blocks) — is excluded from the Discover/Search source list, matched
// by name case-insensitively, while an unrelated source is kept.
func TestService_Sources_ExcludesInfinityScans(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "InfinityScans", Lang: "en"},
			{ID: 2, Name: "infinityscans", Lang: "en"},
			{ID: 3, Name: "MangaDex", Lang: "en"},
		},
	}
	svc := newService(fc)

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Sources: got %d DTOs, want 1 (InfinityScans excluded regardless of case): %+v", len(got), got)
	}
	if got[0].ID != "3" {
		t.Errorf("Sources: got %+v, want only source 3 (MangaDex)", got)
	}
}

// fakeDisabled is an in-memory imports.DisabledSources — the owner-disabled
// source set — for the picker-exclusion tests.
type fakeDisabled struct {
	ids map[int64]bool
	err error
}

func (f fakeDisabled) Disabled(context.Context) (map[int64]bool, error) {
	return f.ids, f.err
}

// TestService_Sources_ExcludesDisabled proves an owner-disabled source (its id
// in the DisabledSources set) is hidden from the Discover/Search "Limit to"
// picker, while an enabled sibling of the same multi-language extension is kept.
func TestService_Sources_ExcludesDisabled(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Webtoons", Lang: "en"},
			{ID: 2, Name: "Webtoons", Lang: "id"},
			{ID: 3, Name: "Webtoons", Lang: "th"},
		},
	}
	svc := newService(fc).WithDisabledSources(fakeDisabled{ids: map[int64]bool{2: true}})

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Sources: got %d DTOs, want 2 (id disabled): %+v", len(got), got)
	}
	for _, s := range got {
		if s.ID == "2" {
			t.Errorf("Sources: disabled source 2 leaked into the picker: %+v", got)
		}
	}
}

// TestService_Sources_DisabledStoreError proves a disabled-store read failure is
// surfaced rather than silently offering a source the owner may have disabled.
func TestService_Sources_DisabledStoreError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("db: disabled read failed")
	fc := &fakeClient{sources: []sourceengine.Source{{ID: 1, Name: "MangaDex", Lang: "en"}}}
	svc := newService(fc).WithDisabledSources(fakeDisabled{err: sentinel})

	if _, err := svc.Sources(context.Background()); !errors.Is(err, sentinel) {
		t.Errorf("Sources: err got %v, want to wrap %v", err, sentinel)
	}
}

// fakeBreakers is an in-memory imports.SourceBreakers — the source circuit-
// breaker snapshot — for the degraded-picker tests. It counts Snapshot calls so
// a test can prove the batch read happens exactly once (no N+1).
type fakeBreakers struct {
	snap  map[string]sourcegate.BreakerState
	err   error
	calls int
}

func (f *fakeBreakers) Snapshot(context.Context) (map[string]sourcegate.BreakerState, error) {
	f.calls++
	return f.snap, f.err
}

// TestService_Sources_FlagsCoolingDownDegraded proves a source whose circuit-
// breaker is currently cooling down is flagged degraded (with a reason), while a
// healthy sibling is not — and that the breaker snapshot is read EXACTLY ONCE
// for the whole page (no per-source lookup / no N+1).
func TestService_Sources_FlagsCoolingDownDegraded(t *testing.T) {
	t.Parallel()

	cooldown := time.Now().Add(30 * time.Minute)
	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Rolia Scan", Lang: "en"},
			{ID: 2, Name: "MangaDex", Lang: "en"},
			{ID: 3, Name: "Comick", Lang: "en"},
		},
	}
	breakers := &fakeBreakers{snap: map[string]sourcegate.BreakerState{
		// Keyed by the trimmed source NAME, the same key sourcegate trips.
		"Rolia Scan": {SourceKey: "Rolia Scan", ConsecutiveFailures: 4, CooldownUntil: &cooldown, LastError: "cf block"},
	}}
	svc := newService(fc).WithSourceBreakers(breakers)

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Sources: got %d DTOs, want 3: %+v", len(got), got)
	}
	byID := map[string]imports.SourceDTO{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if d := byID["1"]; !d.Degraded || d.DegradedReason == "" {
		t.Errorf("Sources: cooling-down source 1 not flagged degraded: %+v", d)
	}
	if d := byID["2"]; d.Degraded || d.DegradedReason != "" {
		t.Errorf("Sources: healthy source 2 wrongly flagged degraded: %+v", d)
	}
	if d := byID["3"]; d.Degraded {
		t.Errorf("Sources: source 3 (no breaker row) wrongly flagged degraded: %+v", d)
	}
	if breakers.calls != 1 {
		t.Errorf("Sources: breaker Snapshot called %d times, want exactly 1 (no N+1)", breakers.calls)
	}
}

// TestService_Sources_ExpiredCooldownNotDegraded proves a breaker row whose
// cooldown has already elapsed is NOT flagged degraded (the source is available
// again) — the flag tracks live cooling-down state, not a historical trip.
func TestService_Sources_ExpiredCooldownNotDegraded(t *testing.T) {
	t.Parallel()

	past := time.Now().Add(-time.Minute)
	fc := &fakeClient{sources: []sourceengine.Source{{ID: 1, Name: "Rolia Scan", Lang: "en"}}}
	breakers := &fakeBreakers{snap: map[string]sourcegate.BreakerState{
		"Rolia Scan": {SourceKey: "Rolia Scan", ConsecutiveFailures: 4, CooldownUntil: &past},
	}}
	svc := newService(fc).WithSourceBreakers(breakers)

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Degraded {
		t.Errorf("Sources: expired-cooldown source wrongly flagged degraded: %+v", got)
	}
}

// TestService_Sources_BreakerSnapshotErrorIsBestEffort proves a breaker-snapshot
// read failure does NOT fail the picker (unlike the disabled-store read): the
// sources are still returned, just without any degraded flag. A missing badge is
// cosmetic; a real fetch against a cooling-down source still 503s from the gate.
func TestService_Sources_BreakerSnapshotErrorIsBestEffort(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{sources: []sourceengine.Source{{ID: 1, Name: "MangaDex", Lang: "en"}}}
	breakers := &fakeBreakers{err: errors.New("db: breaker read failed")}
	svc := newService(fc).WithSourceBreakers(breakers)

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: breaker read failure must not fail the picker, got err: %v", err)
	}
	if len(got) != 1 || got[0].Degraded {
		t.Errorf("Sources: expected 1 un-flagged source on breaker error, got %+v", got)
	}
}

// TestService_Search_ExcludesDisabled proves the Search fan-out never queries an
// owner-disabled source, EVEN when the caller names it explicitly in sourceIDs
// (a stale/hand-crafted request) — resolveSources applies the same exclusion.
func TestService_Search_ExcludesDisabled(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A Source", Lang: "en"},
			{ID: 2, Name: "B Source", Lang: "ko"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Tower of God"}}},
			2: {Manga: []sourceengine.MangaEntry{{Title: "Tower of God"}}},
		},
	}
	svc := newService(fc).WithDisabledSources(fakeDisabled{ids: map[int64]bool{2: true}})

	// Explicitly name BOTH sources; the disabled one must still be dropped, so
	// no candidate from source 2 can appear in any group (a leaked fan-out to
	// source 2 would surface its "Tower of God" hit here).
	got, err := svc.Search(context.Background(), "Tower of God", []string{"1", "2"})
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	seenSource1 := false
	for _, g := range got {
		for _, c := range g.Candidates {
			switch c.Source {
			case "2":
				t.Errorf("Search returned a candidate from disabled source 2: %+v", c)
			case "1":
				seenSource1 = true
			}
		}
	}
	if !seenSource1 {
		t.Fatal("Search dropped the ENABLED source 1 too — the exclusion is over-broad")
	}
}

// --- Search tests ------------------------------------------------------------

// TestService_Search_AllSources verifies that Search(query, nil) fans across
// ALL sources returned by the client.
func TestService_Search_AllSources(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A Source", Lang: "en"},
			{ID: 2, Name: "B Source", Lang: "ko"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Solo Leveling", ThumbnailURL: "http://thumb/1"}}},
			2: {Manga: []sourceengine.MangaEntry{{Title: "Solo Leveling", ThumbnailURL: "http://thumb/2"}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Solo Leveling", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	// Both sources return "Solo Leveling" — matcher should group them into one SearchGroupDTO.
	if len(got) != 1 {
		t.Fatalf("Search: got %d groups, want 1", len(got))
	}
	if len(got[0].Candidates) != 2 {
		t.Fatalf("Search group[0]: got %d candidates, want 2", len(got[0].Candidates))
	}
}

// TestService_Search_FilterSources verifies that Search(query, []string{"1"})
// only queries source "1", not "2".
func TestService_Search_FilterSources(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A Source", Lang: "en"},
			{ID: 2, Name: "B Source", Lang: "ko"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Tower of God"}}},
			2: {Manga: []sourceengine.MangaEntry{{Title: "Tower of God"}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Tower of God", []string{"1"})
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	// Only source "1" queried → only 1 candidate.
	if len(got) != 1 {
		t.Fatalf("Search: got %d groups, want 1", len(got))
	}
	if len(got[0].Candidates) != 1 {
		t.Fatalf("Search group[0]: got %d candidates, want 1", len(got[0].Candidates))
	}
	if got[0].Candidates[0].Source != "1" {
		t.Errorf("Candidate.Source: got %q, want %q", got[0].Candidates[0].Source, "1")
	}
	// SourceName and Lang must be propagated from the resolved source, not left zero.
	if got[0].Candidates[0].SourceName != "A Source" {
		t.Errorf("Candidate.SourceName: got %q, want %q", got[0].Candidates[0].SourceName, "A Source")
	}
	if got[0].Candidates[0].Lang != "en" {
		t.Errorf("Candidate.Lang: got %q, want %q", got[0].Candidates[0].Lang, "en")
	}
}

// TestService_Search_UnknownSourceID verifies that passing a sourceID that does
// not match any client source silently produces an empty, non-nil result —
// resolveSources filters unknown IDs out, so the fan-out has nothing to query.
func TestService_Search_UnknownSourceID(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Real Source", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Some Manga"}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "anything", []string{"nonexistent-id"})
	if err != nil {
		t.Fatalf("Search: unexpected error for unknown source filter: %v", err)
	}
	if got == nil {
		t.Fatal("Search: result must be non-nil even when all requested source IDs are unknown")
	}
	if len(got) != 0 {
		t.Errorf("Search: got %d groups, want 0 — unknown source IDs must be silently dropped", len(got))
	}
}

// TestService_Search_Grouping verifies that two sources returning the same title
// produce one SearchGroupDTO with two candidates, each tagged with its source.
func TestService_Search_Grouping(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Source One", Lang: "en"},
			{ID: 2, Name: "Source Two", Lang: "ko"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Demon Slayer", ThumbnailURL: "http://t/1"}}},
			2: {Manga: []sourceengine.MangaEntry{{Title: "Demon Slayer", ThumbnailURL: "http://t/2"}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Demon Slayer", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Search: got %d groups, want 1 (matcher must group same title)", len(got))
	}
	g := got[0]
	if len(g.Candidates) != 2 {
		t.Fatalf("Group candidates: got %d, want 2", len(g.Candidates))
	}
	// Both candidates must carry their respective source tags.
	sources := map[string]bool{}
	for _, c := range g.Candidates {
		sources[c.Source] = true
	}
	if !sources["1"] || !sources["2"] {
		t.Errorf("Grouping: candidates must carry original source IDs, got %v", sources)
	}
}

// TestService_Search_SourceError verifies that a per-source error is logged and
// skipped — partial results returned, no error surfaced to caller.
func TestService_Search_SourceError(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "OK Source", Lang: "en"},
			{ID: 2, Name: "Bad Source", Lang: "ko"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Naruto"}}},
		},
		searchErrs: map[int64]error{
			2: errors.New("source unreachable"),
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Naruto", nil)
	// No error returned — source failure is partial.
	if err != nil {
		t.Fatalf("Search: expected nil error (partial result), got %v", err)
	}
	// Exactly 1 group from the healthy source.
	if len(got) != 1 {
		t.Fatalf("Search: got %d groups, want 1 (only ok source)", len(got))
	}
	if got[0].Title != "Naruto" {
		t.Errorf("Group.Title: got %q, want %q", got[0].Title, "Naruto")
	}
}

// blockingFakeClient wraps fakeClient and makes Search for one source (blockID)
// hang until its context is cancelled, modelling a Cloudflare-protected source
// that stalls on an anti-bot challenge (a real engine-host HTTP call respects
// ctx, returning as soon as the deadline fires). Every other source delegates
// to the embedded fakeClient and returns immediately.
type blockingFakeClient struct {
	*fakeClient
	blockID int64
}

func (b *blockingFakeClient) Search(ctx context.Context, sourceID int64, query string, page int) (sourceengine.SearchResult, error) {
	if sourceID == b.blockID {
		<-ctx.Done() // hang until the overall Search deadline cancels ctx
		return sourceengine.SearchResult{}, ctx.Err()
	}
	return b.fakeClient.Search(ctx, sourceID, query, page)
}

// TestService_Search_PartialResultsOnDeadline proves the CDN-timeout fix: when a
// source hangs past the overall search deadline, Search returns the fast
// source's results as PARTIAL results — no error, and in roughly the timeout
// (NOT after the hung source's full delay, and NOT hanging forever).
func TestService_Search_PartialResultsOnDeadline(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Fast Source", Lang: "en"},
			{ID: 2, Name: "Slow Source", Lang: "en"}, // hangs until the deadline
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Naruto"}}},
			// source 2 never returns a result — its goroutine blocks on ctx.
		},
	}
	client := &blockingFakeClient{fakeClient: fc, blockID: 2}
	// Short overall deadline so the hung source is dropped quickly.
	svc := imports.NewService(client, nil, nil, "", 200*time.Millisecond, nil)

	start := time.Now()
	got, err := svc.Search(context.Background(), "Naruto", nil)
	elapsed := time.Since(start)

	// Partial results, never an error, never a hang.
	if err != nil {
		t.Fatalf("Search: expected nil error (partial results on deadline), got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Search took %v — deadline should bound it to ~200ms, not the hung source's full delay", elapsed)
	}

	// The fast source's result is present; the slow source's is absent.
	titles := map[string]string{} // title → source id
	for _, grp := range got {
		for _, c := range grp.Candidates {
			titles[c.Title] = c.Source
		}
	}
	if src, ok := titles["Naruto"]; !ok || src != "1" {
		t.Errorf("expected fast source's %q present (source=1), got titles=%v", "Naruto", titles)
	}
	for _, src := range titles {
		if src == "2" {
			t.Errorf("slow (hung) source must be absent from partial results, got %v", titles)
		}
	}
}

// TestService_Search_BlankQuery verifies that a blank query still returns
// groups (the service trusts its input — blank-query validation is the
// handler's responsibility).
func TestService_Search_BlankQuery(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "My Hero Academia"}}},
		},
	}
	svc := newService(fc)

	// Blank query is passed through to the client unchanged.
	got, err := svc.Search(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Search blank query: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Search blank query: got %d groups, want 1", len(got))
	}
}

// TestService_Search_ThumbnailNil verifies that an omitted ThumbnailURL on a
// sourceengine.MangaEntry maps to empty string "" in the SearchCandidateDTO.
func TestService_Search_ThumbnailNil(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "One Piece"}}}, // ThumbnailURL omitted
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "One Piece", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) == 0 || len(got[0].Candidates) == 0 {
		t.Fatal("Search: expected at least one group with one candidate")
	}
	if got[0].Candidates[0].ThumbnailURL != "" {
		t.Errorf("ThumbnailURL: got %q, want empty string for omitted thumbnail", got[0].Candidates[0].ThumbnailURL)
	}
}

// TestService_Search_CandidateFields verifies that SearchCandidateDTO carries
// correct Source, SourceName, Lang, MangaID, Title, and ThumbnailURL fields.
// MangaID is always 0 (the url-addressed engine host assigns no per-manga id).
// ThumbnailURL is the engine host's raw, directly-fetchable value verbatim —
// no Tsundoku-side cover-proxy indirection (P2 Suwayomi-removal).
func TestService_Search_CandidateFields(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Source One", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{
				Title:        "Attack on Titan",
				ThumbnailURL: "http://thumb.test/img.jpg",
				RealURL:      "https://source.test/manga/attack-on-titan",
			}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Attack on Titan", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) == 0 || len(got[0].Candidates) == 0 {
		t.Fatal("expected at least one group and candidate")
	}
	c := got[0].Candidates[0]
	if c.Source != "1" {
		t.Errorf("Candidate.Source: got %q, want %q", c.Source, "1")
	}
	if c.SourceName != "Source One" {
		t.Errorf("Candidate.SourceName: got %q, want %q", c.SourceName, "Source One")
	}
	if c.Lang != "en" {
		t.Errorf("Candidate.Lang: got %q, want %q", c.Lang, "en")
	}
	if c.MangaID != 0 {
		t.Errorf("Candidate.MangaID: got %d, want 0 (url-addressed engine host assigns no manga id)", c.MangaID)
	}
	if c.Title != "Attack on Titan" {
		t.Errorf("Candidate.Title: got %q, want %q", c.Title, "Attack on Titan")
	}
	const wantThumbnail = "http://thumb.test/img.jpg"
	if c.ThumbnailURL != wantThumbnail {
		t.Errorf("Candidate.ThumbnailURL: got %q, want %q (raw engine-host thumbnail URL, no proxy indirection)", c.ThumbnailURL, wantThumbnail)
	}
}

// TestService_Search_CandidateFields_RealURL is the realUrl round-trip proof
// for Search (split out from TestService_Search_CandidateFields to keep that
// function's cyclomatic complexity within the fleet lint budget): the
// browser-clickable "View on source" link is carried straight off
// sourceengine.MangaEntry.RealURL onto SearchCandidateDTO.RealURL, distinct
// from the addressing URL.
func TestService_Search_CandidateFields_RealURL(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Source One", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{
				Title:        "Attack on Titan",
				ThumbnailURL: "http://thumb.test/img.jpg",
				RealURL:      "https://source.test/manga/attack-on-titan",
			}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Attack on Titan", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) == 0 || len(got[0].Candidates) == 0 {
		t.Fatal("expected at least one group and candidate")
	}
	c := got[0].Candidates[0]
	const wantRealURL = "https://source.test/manga/attack-on-titan"
	if c.RealURL != wantRealURL {
		t.Errorf("Candidate.RealURL: got %q, want %q (the browser-clickable View-on-source link, distinct from the addressing url)", c.RealURL, wantRealURL)
	}
}

// --- InspectChapters tests ---------------------------------------------------

// TestService_InspectChapters verifies that InspectChapters maps Chapters to
// []ChapterInspectDTO with the correct number and name.
func TestService_InspectChapters(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		chapters: []sourceengine.Chapter{
			{URL: "/ch/1", Name: "Chapter 1", Number: 1.0},
			{URL: "/ch/2", Name: "Chapter 2", Number: 2.0},
			{URL: "/ch/3", Name: "Special", Number: -1}, // engine host's "unparsed" sentinel
		},
	}
	svc := newService(fc)

	got, err := svc.InspectChapters(context.Background(), "1", "/manga/7", "")
	if err != nil {
		t.Fatalf("InspectChapters: unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("InspectChapters: got %d DTOs, want 3", len(got))
	}

	// Chapter 1
	if got[0].Number == nil || *got[0].Number != 1.0 {
		t.Errorf("InspectChapters[0].Number: got %v, want 1.0", got[0].Number)
	}
	if got[0].Name != "Chapter 1" {
		t.Errorf("InspectChapters[0].Name: got %q, want %q", got[0].Name, "Chapter 1")
	}

	// Chapter 2
	if got[1].Number == nil || *got[1].Number != 2.0 {
		t.Errorf("InspectChapters[1].Number: got %v, want 2.0", got[1].Number)
	}

	// Chapter 3 — unparsed number
	if got[2].Number != nil {
		t.Errorf("InspectChapters[2].Number: got %v, want nil", got[2].Number)
	}
	if got[2].Name != "Special" {
		t.Errorf("InspectChapters[2].Name: got %q, want %q", got[2].Name, "Special")
	}
}

// TestService_InspectChapters_Error verifies that a Chapters error is
// propagated to the caller.
func TestService_InspectChapters_Error(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sourceengine: manga not found")
	fc := &fakeClient{chaptersErr: sentinel}
	svc := newService(fc)

	_, err := svc.InspectChapters(context.Background(), "1", "/manga/99", "")
	if !errors.Is(err, sentinel) {
		t.Errorf("InspectChapters error: got %v, want to wrap %v", err, sentinel)
	}
}

// --- Browse tests ------------------------------------------------------------

// TestService_Browse_Popular verifies the Popular happy path: the resolved
// source's Name/Lang tag each candidate, the manga's url propagates, and
// hasNextPage/page are echoed onto the DTO.
func TestService_Browse_Popular(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Alpha Source", Lang: "en"},
		},
		popularResults: map[int64]sourceengine.SearchResult{
			1: {
				Manga: []sourceengine.MangaEntry{
					{Title: "Solo Leveling", URL: "/manga/1", ThumbnailURL: "http://t/1", RealURL: "https://source.test/manga/solo-leveling"},
					{Title: "Omniscient Reader", URL: "/manga/2"}, // ThumbnailURL + RealURL omitted
				},
				HasNextPage: true,
			},
		},
	}
	svc := newService(fc)

	got, err := svc.Browse(context.Background(), "1", imports.BrowsePopular, 1)
	if err != nil {
		t.Fatalf("Browse: unexpected error: %v", err)
	}
	if len(got.Manga) != 2 {
		t.Fatalf("Browse: got %d candidates, want 2", len(got.Manga))
	}
	if !got.HasNextPage {
		t.Error("Browse: HasNextPage = false, want true")
	}
	if got.Page != 1 {
		t.Errorf("Browse: Page = %d, want 1", got.Page)
	}
	c0 := got.Manga[0]
	assertCandidateTags(t, c0, "1", "Alpha Source", "en")
	if c0.URL != "/manga/1" {
		t.Errorf("Browse candidate[0].URL: got %q, want /manga/1", c0.URL)
	}
	// ThumbnailURL is the raw engine-host value verbatim.
	const wantThumbnail = "http://t/1"
	if c0.ThumbnailURL != wantThumbnail {
		t.Errorf("Browse candidate[0].ThumbnailURL: got %q, want %q", c0.ThumbnailURL, wantThumbnail)
	}
	// realUrl is the browser-clickable "View on source" link, distinct from
	// the addressing url.
	const wantRealURL = "https://source.test/manga/solo-leveling"
	if c0.RealURL != wantRealURL {
		t.Errorf("Browse candidate[0].RealURL: got %q, want %q", c0.RealURL, wantRealURL)
	}
	// Omitted thumbnail → empty string.
	if got.Manga[1].ThumbnailURL != "" {
		t.Errorf("Browse candidate[1].ThumbnailURL: got %q, want empty", got.Manga[1].ThumbnailURL)
	}
	// Omitted realUrl → empty string too.
	if got.Manga[1].RealURL != "" {
		t.Errorf("Browse candidate[1].RealURL: got %q, want empty", got.Manga[1].RealURL)
	}
}

// assertCandidateTags asserts a candidate's source-identity fields (Source,
// SourceName, Lang) — the per-source tags applied during candidate mapping.
func assertCandidateTags(t *testing.T, c imports.SearchCandidateDTO, wantSource, wantName, wantLang string) {
	t.Helper()
	if c.Source != wantSource {
		t.Errorf("candidate.Source: got %q, want %q", c.Source, wantSource)
	}
	if c.SourceName != wantName {
		t.Errorf("candidate.SourceName: got %q, want %q", c.SourceName, wantName)
	}
	if c.Lang != wantLang {
		t.Errorf("candidate.Lang: got %q, want %q", c.Lang, wantLang)
	}
}

// TestService_Browse_Latest verifies the Latest listing dispatches on BrowseType
// and echoes the requested page.
func TestService_Browse_Latest(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 2, Name: "Beta Source", Lang: "ko"},
		},
		latestResults: map[int64]sourceengine.SearchResult{
			2: {
				Manga:       []sourceengine.MangaEntry{{Title: "Tower of God", URL: "/manga/9"}},
				HasNextPage: false,
			},
		},
	}
	svc := newService(fc)

	got, err := svc.Browse(context.Background(), "2", imports.BrowseLatest, 3)
	if err != nil {
		t.Fatalf("Browse latest: unexpected error: %v", err)
	}
	if got.Page != 3 {
		t.Errorf("Browse latest: Page = %d, want 3", got.Page)
	}
	if got.HasNextPage {
		t.Error("Browse latest: HasNextPage = true, want false")
	}
	if len(got.Manga) != 1 || got.Manga[0].URL != "/manga/9" {
		t.Errorf("Browse latest candidates: got %+v", got.Manga)
	}
}

// TestService_Browse_UnknownSource verifies that browsing a source absent from
// the live source list returns ErrSourceNotFound.
func TestService_Browse_UnknownSource(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{{ID: 99, Name: "Real", Lang: "en"}},
	}
	svc := newService(fc)

	_, err := svc.Browse(context.Background(), "ghost", imports.BrowsePopular, 1)
	if !errors.Is(err, imports.ErrSourceNotFound) {
		t.Errorf("Browse unknown source: err = %v, want ErrSourceNotFound", err)
	}
}

// TestService_Browse_UpstreamError verifies that a client Popular/Latest
// failure propagates verbatim — browse is single-source, so a failure is the
// whole request (no partial-results carve-out).
func TestService_Browse_UpstreamError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sourceengine: source offline")
	fc := &fakeClient{
		sources:   []sourceengine.Source{{ID: 1, Name: "Alpha", Lang: "en"}},
		browseErr: sentinel,
	}
	svc := newService(fc)

	_, err := svc.Browse(context.Background(), "1", imports.BrowsePopular, 1)
	if !errors.Is(err, sentinel) {
		t.Errorf("Browse upstream error: err = %v, want to wrap %v", err, sentinel)
	}
}

// TestService_Browse_SourcesError verifies that a failure resolving the source
// list (client.Sources) propagates.
func TestService_Browse_SourcesError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("client: no sources")
	fc := &fakeClient{sourcesErr: sentinel}
	svc := newService(fc)

	// A numeric sourceID is required to reach the client.Sources call inside
	// resolveSource — a non-numeric id short-circuits to ErrSourceNotFound
	// before Sources is ever consulted.
	_, err := svc.Browse(context.Background(), "1", imports.BrowsePopular, 1)
	if !errors.Is(err, sentinel) {
		t.Errorf("Browse sources error: err = %v, want to wrap %v", err, sentinel)
	}
}

// --- MangaDetails --------------------------------------------------------------

// TestService_MangaDetails_OK verifies MangaDetails resolves the source (for
// its Name/Lang tags), calls client.MangaDetails, and maps the enriched
// details through the SAME newCandidateFromDetails/newSearchCandidateDTO
// mappers Search and Browse use — so the returned author/artist/description/
// genres round-trip exactly as they would from those endpoints.
func TestService_MangaDetails_OK(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Alpha Source", Lang: "en"}},
		detailsByURL: map[string]sourceengine.MangaDetails{
			"/manga/1": {
				URL:         "/manga/1",
				Title:       "Solo Leveling",
				Author:      "Chugong",
				Artist:      "Jang Sung-rak",
				Description: "A weak hunter gains power.",
				Genres:      []string{"Action", "Fantasy"},
				RealURL:     "https://source.test/manga/solo-leveling",
			},
		},
	}
	svc := newService(fc)

	got, err := svc.MangaDetails(context.Background(), "1", "/manga/1")
	if err != nil {
		t.Fatalf("MangaDetails: unexpected error: %v", err)
	}
	assertCandidateTags(t, got, "1", "Alpha Source", "en")
	if got.Author != "Chugong" {
		t.Errorf("MangaDetails: Author = %q, want %q", got.Author, "Chugong")
	}
	if got.Artist != "Jang Sung-rak" {
		t.Errorf("MangaDetails: Artist = %q, want %q", got.Artist, "Jang Sung-rak")
	}
	if got.Description != "A weak hunter gains power." {
		t.Errorf("MangaDetails: Description = %q, want %q", got.Description, "A weak hunter gains power.")
	}
	if len(got.Genres) != 2 || got.Genres[0] != "Action" || got.Genres[1] != "Fantasy" {
		t.Errorf("MangaDetails: Genres = %v, want [Action Fantasy]", got.Genres)
	}
	if got.RealURL != "https://source.test/manga/solo-leveling" {
		t.Errorf("MangaDetails: RealURL = %q, want %q", got.RealURL, "https://source.test/manga/solo-leveling")
	}
}

// TestService_MangaDetails_UnknownSource verifies an unresolvable sourceID
// returns ErrSourceNotFound (mirrors TestService_Browse_UnknownSource).
func TestService_MangaDetails_UnknownSource(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{sources: []sourceengine.Source{{ID: 99, Name: "Real", Lang: "en"}}}
	svc := newService(fc)

	_, err := svc.MangaDetails(context.Background(), "ghost", "/manga/1")
	if !errors.Is(err, imports.ErrSourceNotFound) {
		t.Errorf("MangaDetails unknown source: err = %v, want ErrSourceNotFound", err)
	}
}

// TestService_MangaDetails_UpstreamError verifies a client.MangaDetails
// failure propagates verbatim (the handler maps it to 502).
func TestService_MangaDetails_UpstreamError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sourceengine: source unreachable")
	fc := &fakeClient{
		sources:    []sourceengine.Source{{ID: 1, Name: "Alpha", Lang: "en"}},
		detailsErr: sentinel,
	}
	svc := newService(fc)

	_, err := svc.MangaDetails(context.Background(), "1", "/manga/1")
	if !errors.Is(err, sentinel) {
		t.Errorf("MangaDetails upstream error: err = %v, want to wrap %v", err, sentinel)
	}
}

// --- SourceBreakdown tests ----------------------------------------------------

// TestService_SourceBreakdown_GroupsByScanlator verifies the core grouping
// algorithm: chapters split across two named scanlators plus some untagged
// ones group correctly — untagged chapters attribute to the SOURCE NAME (the
// Kaizoku search.go:355 convention), counts/ranges are computed per group via
// the shared FormatChapterRanges helper, Total counts every chapter, and the
// result is sorted by Count descending (ties by Scanlator name ascending).
func TestService_SourceBreakdown_GroupsByScanlator(t *testing.T) {
	t.Parallel()

	const url = "/manga/1"
	fc := &fakeClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Alpha Source", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{
			url: {
				{URL: "/ch/1", Number: 1, Scanlator: "Alpha Scans"},
				{URL: "/ch/2", Number: 2, Scanlator: "Alpha Scans"},
				{URL: "/ch/3", Number: 3, Scanlator: "Alpha Scans"},
				{URL: "/ch/4", Number: 1, Scanlator: "Beta Scans"},
				{URL: "/ch/5", Number: 2, Scanlator: "Beta Scans"},
				{URL: "/ch/6", Number: 1, Scanlator: ""}, // untagged → source name
			},
		},
	}
	svc := newService(fc)

	got, err := svc.SourceBreakdown(context.Background(), "1", url, "")
	if err != nil {
		t.Fatalf("SourceBreakdown: unexpected error: %v", err)
	}
	if got.Total != 6 {
		t.Errorf("SourceBreakdown: Total = %d, want 6", got.Total)
	}
	if len(got.Scanlators) != 3 {
		t.Fatalf("SourceBreakdown: got %d groups, want 3", len(got.Scanlators))
	}

	// Sorted by Count descending: Alpha Scans (3), Beta Scans (2), Alpha Source (1).
	want := []imports.ScanlatorCoverageDTO{
		{Scanlator: "Alpha Scans", Count: 3, Ranges: "1-3"},
		{Scanlator: "Beta Scans", Count: 2, Ranges: "1-2"},
		{Scanlator: "Alpha Source", Count: 1, Ranges: "1"},
	}
	for i, w := range want {
		if got.Scanlators[i] != w {
			t.Errorf("SourceBreakdown.Scanlators[%d]: got %+v, want %+v", i, got.Scanlators[i], w)
		}
	}
}

// TestService_SourceBreakdown_SortTiesByName verifies that groups with equal
// counts are ordered by Scanlator name ascending (deterministic tie-break).
func TestService_SourceBreakdown_SortTiesByName(t *testing.T) {
	t.Parallel()

	const url = "/manga/1"
	fc := &fakeClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Alpha Source", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{
			url: {
				{URL: "/ch/1", Number: 1, Scanlator: "Zeta Scans"},
				{URL: "/ch/2", Number: 1, Scanlator: "Alpha Scans"},
			},
		},
	}
	svc := newService(fc)

	got, err := svc.SourceBreakdown(context.Background(), "1", url, "")
	if err != nil {
		t.Fatalf("SourceBreakdown: unexpected error: %v", err)
	}
	if len(got.Scanlators) != 2 {
		t.Fatalf("SourceBreakdown: got %d groups, want 2", len(got.Scanlators))
	}
	if got.Scanlators[0].Scanlator != "Alpha Scans" || got.Scanlators[1].Scanlator != "Zeta Scans" {
		t.Errorf("SourceBreakdown: tie order = [%q, %q], want [Alpha Scans, Zeta Scans]",
			got.Scanlators[0].Scanlator, got.Scanlators[1].Scanlator)
	}
}

// TestService_SourceBreakdown_UnknownSource verifies an unresolvable sourceID
// returns ErrSourceNotFound (mirrors MangaDetails/Browse).
func TestService_SourceBreakdown_UnknownSource(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{sources: []sourceengine.Source{{ID: 99, Name: "Real", Lang: "en"}}}
	svc := newService(fc)

	_, err := svc.SourceBreakdown(context.Background(), "ghost", "/manga/1", "")
	if !errors.Is(err, imports.ErrSourceNotFound) {
		t.Errorf("SourceBreakdown unknown source: err = %v, want ErrSourceNotFound", err)
	}
}

// TestService_SourceBreakdown_UpstreamError verifies a client.Chapters
// failure propagates verbatim (the handler maps it to 502).
func TestService_SourceBreakdown_UpstreamError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sourceengine: source unreachable")
	const url = "/manga/1"
	fc := &fakeClient{
		sources:          []sourceengine.Source{{ID: 1, Name: "Alpha", Lang: "en"}},
		chapterErrsByURL: map[string]error{url: sentinel},
	}
	svc := newService(fc)

	_, err := svc.SourceBreakdown(context.Background(), "1", url, "")
	if !errors.Is(err, sentinel) {
		t.Errorf("SourceBreakdown upstream error: err = %v, want to wrap %v", err, sentinel)
	}
}

// TestService_Search_URLPopulated is the non-vacuous proof that Search now
// surfaces the manga url on each candidate — removing the URL mapping fails it.
func TestService_Search_URLPopulated(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Attack on Titan", URL: "/manga/42"}}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Attack on Titan", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) == 0 || len(got[0].Candidates) == 0 {
		t.Fatal("expected at least one group and candidate")
	}
	if got[0].Candidates[0].URL != "/manga/42" {
		t.Errorf("Search candidate URL: got %q, want /manga/42", got[0].Candidates[0].URL)
	}
}

// --- Adopt tests (Task 4) — require testdb (Docker) --------------------------
//
// newServiceDB constructs a Service with a real testdb-backed Ingest and the
// given fakeClient. Storage is left empty because Adopt does not touch disk.
func newServiceDB(t *testing.T, fc *fakeClient) *imports.Service {
	t.Helper()
	db := testdb.New(t)
	ingestSvc := ingest.NewIngest(fc, db)
	return imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)
}

// assertAdoptSeries verifies that exactly one Series exists with the expected
// slug (derived from title) and that its ID matches the returned UUID.
func assertAdoptSeries(t *testing.T, ctx context.Context, db *ent.Client, title string, wantID fmt.Stringer) {
	t.Helper()
	series := db.Series.Query().AllX(ctx)
	if len(series) != 1 {
		t.Fatalf("Series count: got %d, want 1", len(series))
	}
	wantSlug := disk.Slugify(title)
	if series[0].Slug != wantSlug {
		t.Errorf("Series.Slug: got %q, want %q", series[0].Slug, wantSlug)
	}
	if series[0].ID.String() != wantID.String() {
		t.Errorf("Series.ID: got %s, want %s", series[0].ID, wantID.String())
	}
}

// assertAdoptProviders checks that exactly wantCount SeriesProvider rows exist
// and that each (provider, importance) pair in wantImportances is satisfied.
func assertAdoptProviders(t *testing.T, ctx context.Context, db *ent.Client, wantCount int, wantImportances map[string]int) {
	t.Helper()
	providers := db.SeriesProvider.Query().AllX(ctx)
	if len(providers) != wantCount {
		t.Fatalf("SeriesProvider count: got %d, want %d", len(providers), wantCount)
	}
	got := make(map[string]int, len(providers))
	for _, sp := range providers {
		got[sp.Provider] = sp.Importance
	}
	for src, imp := range wantImportances {
		if got[src] != imp {
			t.Errorf("Provider %q importance: got %d, want %d", src, got[src], imp)
		}
	}
}

// assertAdoptChapters checks that exactly wantCount Chapter rows exist and that
// all are in state "wanted".
func assertAdoptChapters(t *testing.T, ctx context.Context, db *ent.Client, wantCount int) {
	t.Helper()
	chapters := db.Chapter.Query().AllX(ctx)
	if len(chapters) != wantCount {
		t.Fatalf("Chapter count: got %d, want %d", len(chapters), wantCount)
	}
	for _, ch := range chapters {
		if ch.State != "wanted" {
			t.Errorf("Chapter %q: state got %q, want wanted", ch.ChapterKey, ch.State)
		}
	}
}

// TestService_Adopt_TwoProviders verifies the canonical Adopt case: two
// providers with DIFFERENT per-source titles (resolved via MangaDetails) are
// adopted under one canonical title "Solo Leveling" → exactly ONE Series row
// (slug = disk.Slugify("Solo Leveling")), TWO SeriesProvider rows with correct
// importances, and chapters in state wanted.
func TestService_Adopt_TwoProviders(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Solo Leveling"
		srcA           = "1"
		urlA           = "/manga/101"
		impA           = 10 // higher importance → ranked first
		srcB           = "2"
		urlB           = "/manga/202"
		impB           = 5
	)

	fc := &fakeClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			urlA: makeAdoptChapters(urlA, 2),
			urlB: makeAdoptChapters(urlB, 3),
		},
	}
	ingestSvc := ingest.NewIngest(fc, db)
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	id, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: srcA, URL: urlA, Importance: impA},
			{Source: srcB, URL: urlB, Importance: impB},
		},
	})
	if err != nil {
		t.Fatalf("Adopt: unexpected error: %v", err)
	}
	if id.String() == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("Adopt: returned zero UUID")
	}

	assertAdoptSeries(t, ctx, db, canonicalTitle, id)
	assertAdoptProviders(t, ctx, db, 2, map[string]int{srcA: impA, srcB: impB})
	assertAdoptChapters(t, ctx, db, 3)
}

// TestService_Adopt_UngatedBypassesTrippedBreaker proves Adopt uses the UNGATED
// attach (ingest.AddSeriesUngated): a per-source circuit-breaker tripped by
// unrelated BULK background failures must NOT block a deliberate owner adopt
// (QCAT-281). The same breaker still refuses the GATED path (AddSeries) that the
// refresh sweep + download dispatcher use — proving only the owner click is
// exempt, gating stays in force for anti-ban.
func TestService_Adopt_UngatedBypassesTrippedBreaker(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		title  = "Solo Leveling"
		src    = "1"
		url    = "/manga/101"
		srcKey = "Comix" // the source's display name = the breaker key
	)
	fc := &fakeClient{
		sources:       []sourceengine.Source{{ID: 1, Name: srcKey, Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{url: makeAdoptChapters(url, 2)},
	}

	// Trip the breaker for the source's display name via the gate itself.
	gate := sourcegate.NewService(db, settings.Static{SourcesFailureThresh: 3, SourcesCooldownIv: 10 * time.Minute})
	now := time.Now()
	for i := 0; i < 3; i++ {
		gate.RecordFailure(ctx, srcKey, errors.New("cf block"), now)
	}
	if gate.IsAvailable(ctx, srcKey, now) {
		t.Fatal("test setup: expected the breaker tripped")
	}

	ingestSvc := ingest.NewIngestWithGate(fc, db, nil, gate)

	// The GATED path (sweeps / dispatcher) IS refused by the tripped breaker.
	if _, err := ingestSvc.AddSeries(ctx, 1, url, title, ""); !errors.Is(err, ingest.ErrSourceCooledDown) {
		t.Fatalf("gated AddSeries must be refused by the tripped breaker, got %v", err)
	}

	// Adopt (owner click) uses the UNGATED attach → succeeds despite the trip.
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)
	id, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title:     title,
		Providers: []imports.AdoptProvider{{Source: src, URL: url, Importance: 10}},
	})
	if err != nil {
		t.Fatalf("Adopt must succeed via the ungated attach despite a tripped breaker: %v", err)
	}
	if id.String() == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("Adopt returned zero UUID")
	}
	assertAdoptProviders(t, ctx, db, 1, map[string]int{src: 10})
}

// TestService_Adopt_SameSourceDifferentScanlators is the CRITICAL
// setImportances-by-scanlator proof: two AdoptProviders naming the SAME
// source under two DIFFERENT scanlators, with DIFFERENT importances, must
// produce TWO SeriesProvider rows — each keeping its OWN importance. Before
// the fix, setImportances matched by (seriesID, provider) alone, so both
// rows would collapse onto whichever one First(ctx) happened to return.
func TestService_Adopt_SameSourceDifferentScanlators(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Comix Series"
		src            = "1"
		url            = "/manga/555"
		scanA          = "Alpha Scans"
		impA           = 5
		scanB          = "Beta Scans"
		impB           = 3
	)

	chapsA := makeAdoptChapters(url, 2) // numbers 1, 2
	for i := range chapsA {
		chapsA[i].Scanlator = scanA
	}
	// Numbers 3, 4, 5 — DISJOINT from chapsA's 1, 2, so the union has exactly
	// 5 distinct chapter_keys (Chapter identity is per-series, not per-
	// provider — overlapping numbers here would dedupe and defeat the count
	// assertion below).
	chapsB := makeAdoptChapters(url, 3)
	for i := range chapsB {
		chapsB[i].Number += 2
		chapsB[i].Scanlator = scanB
	}

	fc := &fakeClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			// Both AdoptProviders name the SAME source+url, so both AddSeries
			// calls fetch this SAME combined (unfiltered) list — mirroring
			// production's single upstream feed for one source-manga, split
			// downstream by each provider's own scanlator filter.
			url: append(append([]sourceengine.Chapter{}, chapsA...), chapsB...),
		},
	}
	ingestSvc := ingest.NewIngest(fc, db)
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	id, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: src, URL: url, Importance: impA, Scanlator: scanA},
			{Source: src, URL: url, Importance: impB, Scanlator: scanB},
		},
	})
	if err != nil {
		t.Fatalf("Adopt: unexpected error: %v", err)
	}

	assertAdoptSeries(t, ctx, db, canonicalTitle, id)

	rows := db.SeriesProvider.Query().AllX(ctx)
	if len(rows) != 2 {
		t.Fatalf("SeriesProvider count: got %d, want 2", len(rows))
	}
	gotImportance := make(map[string]int, len(rows))
	for _, sp := range rows {
		if sp.Provider != src {
			t.Errorf("SeriesProvider.Provider: got %q, want %q", sp.Provider, src)
		}
		gotImportance[sp.Scanlator] = sp.Importance
	}
	if gotImportance[scanA] != impA {
		t.Errorf("%s/%s importance: got %d, want %d", src, scanA, gotImportance[scanA], impA)
	}
	if gotImportance[scanB] != impB {
		t.Errorf("%s/%s importance: got %d, want %d", src, scanB, gotImportance[scanB], impB)
	}

	// Chapters from both scanlators ingested (2 + 3 = 5), each filtered into
	// its own provider's feed by ingest.Ingest's scanlator filter.
	assertAdoptChapters(t, ctx, db, 5)
}

// TestService_Adopt_Idempotent verifies that calling Adopt twice with the same
// request produces no new Series/SeriesProvider/Chapter rows on the second call.
func TestService_Adopt_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Tower of God"
		srcA           = "1"
		urlA           = "/manga/301"
		impA           = 10
	)

	fc := &fakeClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			urlA: makeAdoptChapters(urlA, 2),
		},
	}
	ingestSvc := ingest.NewIngest(fc, db)
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	req := imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: srcA, URL: urlA, Importance: impA},
		},
	}

	// First call.
	if _, err := svc.Adopt(ctx, req); err != nil {
		t.Fatalf("first Adopt: %v", err)
	}

	countSeries := len(db.Series.Query().AllX(ctx))
	countProviders := len(db.SeriesProvider.Query().AllX(ctx))
	countChapters := len(db.Chapter.Query().AllX(ctx))

	// Second call: must be idempotent — no new rows.
	if _, err := svc.Adopt(ctx, req); err != nil {
		t.Fatalf("second Adopt: %v", err)
	}

	if n := len(db.Series.Query().AllX(ctx)); n != countSeries {
		t.Errorf("Series count after second Adopt: got %d, want %d", n, countSeries)
	}
	if n := len(db.SeriesProvider.Query().AllX(ctx)); n != countProviders {
		t.Errorf("SeriesProvider count after second Adopt: got %d, want %d", n, countProviders)
	}
	if n := len(db.Chapter.Query().AllX(ctx)); n != countChapters {
		t.Errorf("Chapter count after second Adopt: got %d, want %d", n, countChapters)
	}
}

// TestService_Adopt_AttachToExisting verifies that adopting a second source for
// an already-adopted canonical title adds just one more SeriesProvider to the
// existing series without duplicating the series row or existing chapters.
func TestService_Adopt_AttachToExisting(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Vinland Saga"
		srcA           = "1"
		urlA           = "/manga/401"
		impA           = 10
		srcB           = "2"
		urlB           = "/manga/402"
		impB           = 5
	)

	fc := &fakeClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			urlA: makeAdoptChapters(urlA, 2),
			urlB: makeAdoptChapters(urlB, 1),
		},
	}
	ingestSvc := ingest.NewIngest(fc, db)
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	// First adopt: one provider.
	if _, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title:     canonicalTitle,
		Providers: []imports.AdoptProvider{{Source: srcA, URL: urlA, Importance: impA}},
	}); err != nil {
		t.Fatalf("first Adopt: %v", err)
	}

	seriesCount1 := len(db.Series.Query().AllX(ctx))
	if seriesCount1 != 1 {
		t.Fatalf("after first Adopt: Series count got %d, want 1", seriesCount1)
	}

	// Second adopt: new provider for the same series.
	if _, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title:     canonicalTitle,
		Providers: []imports.AdoptProvider{{Source: srcB, URL: urlB, Importance: impB}},
	}); err != nil {
		t.Fatalf("second Adopt (attach): %v", err)
	}

	// Still ONE Series.
	if n := len(db.Series.Query().AllX(ctx)); n != 1 {
		t.Errorf("Series count after attach: got %d, want 1", n)
	}
	// TWO SeriesProviders.
	if n := len(db.SeriesProvider.Query().AllX(ctx)); n != 2 {
		t.Errorf("SeriesProvider count after attach: got %d, want 2", n)
	}
	// Both providers should carry correct importances.
	providers := db.SeriesProvider.Query().AllX(ctx)
	impByProvider := make(map[string]int, 2)
	for _, sp := range providers {
		impByProvider[sp.Provider] = sp.Importance
	}
	if impByProvider[srcA] != impA {
		t.Errorf("Provider %q importance: got %d, want %d", srcA, impByProvider[srcA], impA)
	}
	if impByProvider[srcB] != impB {
		t.Errorf("Provider %q importance: got %d, want %d", srcB, impByProvider[srcB], impB)
	}
}

// TestService_Adopt_Category verifies that a non-empty Category in AdoptRequest
// sets Series.category, and that an empty Category leaves it at the default Other.
func TestService_Adopt_Category(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		url      string
		source   string
		title    string
		category string // request Category ("" ⇒ keep the default)
		wantCat  string // expected Series category name after adopt
	}{
		{name: "set_category", url: "/manga/501", source: "1", title: "Berserk", category: "Manga", wantCat: "Manga"},
		{name: "default_category", url: "/manga/502", source: "2", title: "Naruto", category: "", wantCat: "Other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testdb.New(t)
			fc := &fakeClient{
				chaptersByURL: map[string][]sourceengine.Chapter{
					tc.url: makeAdoptChapters(tc.url, 1),
				},
			}
			ingestSvc := ingest.NewIngest(fc, db)
			svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

			_, err := svc.Adopt(ctx, imports.AdoptRequest{
				Title:     tc.title,
				Category:  tc.category,
				Providers: []imports.AdoptProvider{{Source: tc.source, URL: tc.url, Importance: 1}},
			})
			if err != nil {
				t.Fatalf("Adopt: %v", err)
			}

			s := db.Series.Query().OnlyX(ctx)
			if name := s.QueryCategory().OnlyX(ctx).Name; name != tc.wantCat {
				t.Errorf("Series category: got %q, want %q", name, tc.wantCat)
			}
		})
	}
}

// TestService_Adopt_NoSilentPartial verifies §16: when one provider's AddSeries
// errors mid-group, Adopt returns a non-nil error naming the source(s) already
// attached in this call. The successful provider's rows ARE present (no rollback).
func TestService_Adopt_NoSilentPartial(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Demon Slayer"
		srcOK          = "1"
		urlOK          = "/manga/601"
		impOK          = 10
		srcFail        = "2"
		urlFail        = "/manga/602"
		impFail        = 5
	)

	injectErr := errors.New("sourceengine: source unavailable")
	fc := &fakeClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			urlOK: makeAdoptChapters(urlOK, 2),
		},
		chapterErrsByURL: map[string]error{
			urlFail: injectErr,
		},
	}
	ingestSvc := ingest.NewIngest(fc, db)
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	req := imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: srcOK, URL: urlOK, Importance: impOK},
			{Source: srcFail, URL: urlFail, Importance: impFail},
		},
	}

	_, err := svc.Adopt(ctx, req)
	if err == nil {
		t.Fatal("Adopt: expected non-nil error for mid-group provider failure, got nil")
	}

	// Error message must name the already-attached source.
	if !strings.Contains(err.Error(), srcOK) {
		t.Errorf("Adopt error %q: must name already-attached source %q", err.Error(), srcOK)
	}

	// The successful provider's rows MUST be present (no rollback).
	seriesList := db.Series.Query().AllX(ctx)
	if len(seriesList) != 1 {
		t.Fatalf("Series count after partial failure: got %d, want 1 (successful provider must be persisted)", len(seriesList))
	}
	spList := db.SeriesProvider.Query().
		Where(entseriesprovider.Provider(srcOK)).
		AllX(ctx)
	if len(spList) != 1 {
		t.Fatalf("SeriesProvider for %q: got %d, want 1", srcOK, len(spList))
	}
}

// TestService_Adopt_InvalidCategory verifies that an invalid Category value in
// AdoptRequest returns a non-nil error before any DB rows are created.
func TestService_Adopt_InvalidCategory(t *testing.T) {
	ctx := context.Background()

	const url = "/manga/701"
	fc := &fakeClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			url: makeAdoptChapters(url, 1),
		},
	}
	svc := newServiceDB(t, fc)

	_, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: "One Piece",
		// Categories are user-defined; "invalid" now means filesystem-unsafe (it
		// becomes a folder name), not "not in a fixed enum".
		Category: "bad/name",
		Providers: []imports.AdoptProvider{
			{Source: "1", URL: url, Importance: 1},
		},
	})
	if err == nil {
		t.Fatal("Adopt: expected error for invalid category, got nil")
	}
}
