// Package imports_test — unit tests for Service (Sources, Search, InspectChapters, Adopt).
//
// Task 3 tests use an in-process fakeClient; no Suwayomi process, no network, no DB.
// Task 4 Adopt tests additionally require testdb (ephemeral Postgres via Docker).
package imports_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// --- fake client -------------------------------------------------------------

// fakeClient implements suwayomi.Client with canned per-source responses.
// Methods unused by Service return nil, nil.
//
// For Adopt tests (Task 4) the client dispatches FetchChapters by mangaID:
//   - chaptersPerManga maps mangaID → chapters to return.
//   - chapterErrs maps mangaID → error to return (takes priority).
//
// The original flat chapters/chaptersErr fields remain for Task 3 compatibility;
// if chaptersPerManga is non-nil it takes priority over the flat fields.
type fakeClient struct {
	// sources is the slice returned by Sources.
	sources []suwayomi.Source
	// sourcesErr is the error returned by Sources (nil = success).
	sourcesErr error
	// searchResults maps sourceID → results returned by Search.
	searchResults map[string][]suwayomi.Manga
	// searchErrs maps sourceID → error returned by Search (nil = success).
	searchErrs map[string]error
	// chapters is the slice returned by FetchChapters (Task 3 flat path).
	chapters []suwayomi.Chapter
	// chaptersErr is the error returned by FetchChapters (Task 3 flat path).
	chaptersErr error
	// chaptersPerManga maps mangaID → chapters (Task 4 per-manga path).
	// Non-nil activates the per-manga dispatch.
	chaptersPerManga map[int][]suwayomi.Chapter
	// chapterErrs maps mangaID → error (Task 4 per-manga error injection).
	chapterErrs map[int]error
	// browseResults maps BrowseType → result returned by Browse.
	browseResults map[suwayomi.BrowseType]suwayomi.BrowseResult
	// browseErr is the error returned by Browse (nil = success).
	browseErr error
}

func (f *fakeClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	return f.sources, f.sourcesErr
}

func (f *fakeClient) Search(_ context.Context, sourceID, _ string) ([]suwayomi.Manga, error) {
	if f.searchErrs != nil {
		if err, ok := f.searchErrs[sourceID]; ok {
			return nil, err
		}
	}
	if f.searchResults != nil {
		if res, ok := f.searchResults[sourceID]; ok {
			return res, nil
		}
	}
	return nil, nil
}

func (f *fakeClient) FetchChapters(_ context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	// Per-manga dispatch (Task 4): error first, then chapters.
	if f.chapterErrs != nil {
		if err, ok := f.chapterErrs[mangaID]; ok {
			return nil, err
		}
	}
	if f.chaptersPerManga != nil {
		return f.chaptersPerManga[mangaID], nil
	}
	// Flat fallback (Task 3).
	return f.chapters, f.chaptersErr
}

func (f *fakeClient) Browse(_ context.Context, _ string, t suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	if f.browseErr != nil {
		return suwayomi.BrowseResult{}, f.browseErr
	}
	if f.browseResults != nil {
		return f.browseResults[t], nil
	}
	return suwayomi.BrowseResult{}, nil
}

// Remaining Client methods are unused by Service; return nil, nil.
func (f *fakeClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	return nil, nil
}
func (f *fakeClient) PageBytes(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}
func (f *fakeClient) ServerSettings(_ context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeClient) SetServerSettings(_ context.Context, _ suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeClient) Extensions(_ context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (f *fakeClient) SetExtensionState(_ context.Context, _ string, _ suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeClient) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeClient) ExtensionRepos(_ context.Context) ([]string, error)    { return nil, nil }
func (f *fakeClient) SetExtensionRepos(_ context.Context, _ []string) error { return nil }

// --- helpers -----------------------------------------------------------------

// ptrStr returns a pointer to s.
func ptrStr(s string) *string { return &s }

// ptrF64 returns a pointer to v.
func ptrF64(v float64) *float64 { return &v }

// newService constructs a Service with a fake client and nil ingest/db (unused in Task 3).
func newService(fc *fakeClient) *imports.Service {
	return imports.NewService(fc, nil, nil, "")
}

// makeAdoptChapters builds n stub suwayomi.Chapter values anchored to a base ID
// so that distinct mangaIDs get non-overlapping suwayomi chapter IDs. Each
// chapter has a sequential chapter number so that NormalizeChapterKey produces
// distinct, deterministic keys.
func makeAdoptChapters(baseID, n int) []suwayomi.Chapter {
	chs := make([]suwayomi.Chapter, n)
	for i := range n {
		num := float64(i + 1)
		numCopy := num
		chs[i] = suwayomi.Chapter{
			ID:     baseID + i,
			Index:  i,
			Name:   fmt.Sprintf("Chapter %.0f", num),
			Number: &numCopy,
			URL:    fmt.Sprintf("https://test/ch/%d", i+1),
		}
	}
	return chs
}

// --- Sources tests -----------------------------------------------------------

// TestService_Sources verifies that Sources maps the client list to []SourceDTO.
func TestService_Sources(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "src-a", Name: "Alpha Source", Lang: "en"},
			{ID: "src-b", Name: "Beta Source", Lang: "ko"},
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
	if got[0].ID != "src-a" || got[0].Name != "Alpha Source" || got[0].Lang != "en" {
		t.Errorf("Sources[0]: got %+v, want {src-a Alpha Source en}", got[0])
	}
	if got[1].ID != "src-b" || got[1].Name != "Beta Source" || got[1].Lang != "ko" {
		t.Errorf("Sources[1]: got %+v, want {src-b Beta Source ko}", got[1])
	}
}

// TestService_Sources_ExcludesLocalSource verifies that Suwayomi's built-in
// Local source (id suwayomi.LocalSourceID, lang "localsourcelang") is dropped
// from the returned list — it is a Suwayomi-internal on-disk source, not a
// real content source, and should never populate the Discover/Search source
// pickers (F1). A source matching either signal (id or lang, case-insensitive)
// is excluded; real sources are kept untouched.
func TestService_Sources_ExcludesLocalSource(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: suwayomi.LocalSourceID, Name: "Local source", Lang: "localsourcelang"},
			{ID: "src-a", Name: "Alpha Source", Lang: "en"},
			// A source that only matches on the lang signal (id changed, lang
			// unchanged) must still be excluded — the defensive secondary match.
			{ID: "999", Name: "Local source", Lang: "LOCALSOURCELANG"},
			{ID: "src-b", Name: "Beta Source", Lang: "ko"},
		},
	}
	svc := newService(fc)

	got, err := svc.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Sources: got %d DTOs, want 2 (local source excluded): %+v", len(got), got)
	}
	for _, dto := range got {
		if dto.ID == suwayomi.LocalSourceID || strings.EqualFold(dto.Lang, "localsourcelang") {
			t.Errorf("Sources: local source leaked into result: %+v", dto)
		}
	}
	if got[0].ID != "src-a" || got[1].ID != "src-b" {
		t.Errorf("Sources: got %+v, want [src-a, src-b]", got)
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

// --- Search tests ------------------------------------------------------------

// TestService_Search_AllSources verifies that Search(query, nil) fans across
// ALL sources returned by the client.
func TestService_Search_AllSources(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "a", Name: "A Source", Lang: "en"},
			{ID: "b", Name: "B Source", Lang: "ko"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"a": {{ID: 1, Title: "Solo Leveling", ThumbnailURL: ptrStr("http://thumb/1")}},
			"b": {{ID: 2, Title: "Solo Leveling", ThumbnailURL: ptrStr("http://thumb/2")}},
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

// TestService_Search_FilterSources verifies that Search(query, []string{"a"})
// only queries source "a", not "b".
func TestService_Search_FilterSources(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "a", Name: "A Source", Lang: "en"},
			{ID: "b", Name: "B Source", Lang: "ko"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"a": {{ID: 1, Title: "Tower of God"}},
			"b": {{ID: 2, Title: "Tower of God"}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Tower of God", []string{"a"})
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	// Only source "a" queried → only 1 candidate.
	if len(got) != 1 {
		t.Fatalf("Search: got %d groups, want 1", len(got))
	}
	if len(got[0].Candidates) != 1 {
		t.Fatalf("Search group[0]: got %d candidates, want 1", len(got[0].Candidates))
	}
	if got[0].Candidates[0].Source != "a" {
		t.Errorf("Candidate.Source: got %q, want %q", got[0].Candidates[0].Source, "a")
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
		sources: []suwayomi.Source{
			{ID: "real-src", Name: "Real Source", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"real-src": {{ID: 1, Title: "Some Manga"}},
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
		sources: []suwayomi.Source{
			{ID: "src1", Name: "Source One", Lang: "en"},
			{ID: "src2", Name: "Source Two", Lang: "ko"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"src1": {{ID: 10, Title: "Demon Slayer", ThumbnailURL: ptrStr("http://t/1")}},
			"src2": {{ID: 20, Title: "Demon Slayer", ThumbnailURL: ptrStr("http://t/2")}},
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
	if !sources["src1"] || !sources["src2"] {
		t.Errorf("Grouping: candidates must carry original source IDs, got %v", sources)
	}
}

// TestService_Search_SourceError verifies that a per-source error is logged and
// skipped — partial results returned, no error surfaced to caller.
func TestService_Search_SourceError(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "ok", Name: "OK Source", Lang: "en"},
			{ID: "bad", Name: "Bad Source", Lang: "ko"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"ok": {{ID: 1, Title: "Naruto"}},
		},
		searchErrs: map[string]error{
			"bad": errors.New("source unreachable"),
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

// TestService_Search_BlankQuery verifies that a blank query still returns
// groups (the service trusts its input — blank-query validation is the
// handler's responsibility).
func TestService_Search_BlankQuery(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "a", Name: "A", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"a": {{ID: 1, Title: "My Hero Academia"}},
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

// TestService_Search_ThumbnailNil verifies that a nil ThumbnailURL on a
// suwayomi.Manga maps to empty string "" in the SearchCandidateDTO.
func TestService_Search_ThumbnailNil(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "a", Name: "A", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"a": {{ID: 1, Title: "One Piece", ThumbnailURL: nil}},
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
		t.Errorf("ThumbnailURL: got %q, want empty string for nil thumbnail", got[0].Candidates[0].ThumbnailURL)
	}
}

// TestService_Search_CandidateFields verifies that SearchCandidateDTO carries
// correct Source, SourceName, Lang, MangaID, Title, and ThumbnailURL fields.
// ThumbnailURL must be Tsundoku's OWN cover-proxy path, not Suwayomi's raw
// thumbnail URL (B2 fix — the raw value 404s against Tsundoku's own origin).
func TestService_Search_CandidateFields(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "s1", Name: "Source One", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"s1": {{ID: 42, Title: "Attack on Titan", ThumbnailURL: ptrStr("http://thumb.test/img.jpg")}},
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
	if c.Source != "s1" {
		t.Errorf("Candidate.Source: got %q, want %q", c.Source, "s1")
	}
	if c.SourceName != "Source One" {
		t.Errorf("Candidate.SourceName: got %q, want %q", c.SourceName, "Source One")
	}
	if c.Lang != "en" {
		t.Errorf("Candidate.Lang: got %q, want %q", c.Lang, "en")
	}
	if c.MangaID != 42 {
		t.Errorf("Candidate.MangaID: got %d, want %d", c.MangaID, 42)
	}
	if c.Title != "Attack on Titan" {
		t.Errorf("Candidate.Title: got %q, want %q", c.Title, "Attack on Titan")
	}
	const wantProxyPath = "/api/sources/s1/manga/42/cover"
	if c.ThumbnailURL != wantProxyPath {
		t.Errorf("Candidate.ThumbnailURL: got %q, want %q (Tsundoku cover-proxy path, not the raw Suwayomi URL)", c.ThumbnailURL, wantProxyPath)
	}
}

// TestService_Search_MetadataFields verifies that author/artist/genres/
// description propagate from suwayomi.Manga onto the SearchCandidateDTO (M4).
func TestService_Search_MetadataFields(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "s1", Name: "Source One", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"s1": {{
				ID:          42,
				Title:       "Vinland Saga",
				Author:      ptrStr("Makoto Yukimura"),
				Artist:      ptrStr("Makoto Yukimura"),
				Description: ptrStr("A Viking's saga."),
				Genre:       []string{"Action", "Historical"},
			}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "Vinland Saga", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) == 0 || len(got[0].Candidates) == 0 {
		t.Fatal("expected at least one group and candidate")
	}
	c := got[0].Candidates[0]
	if c.Author != "Makoto Yukimura" {
		t.Errorf("Candidate.Author: got %q, want %q", c.Author, "Makoto Yukimura")
	}
	if c.Artist != "Makoto Yukimura" {
		t.Errorf("Candidate.Artist: got %q, want %q", c.Artist, "Makoto Yukimura")
	}
	if c.Description != "A Viking's saga." {
		t.Errorf("Candidate.Description: got %q, want %q", c.Description, "A Viking's saga.")
	}
	if len(c.Genres) != 2 || c.Genres[0] != "Action" || c.Genres[1] != "Historical" {
		t.Errorf("Candidate.Genres: got %v, want [Action Historical]", c.Genres)
	}
}

// TestService_Search_MetadataFieldsNil verifies that nil author/artist/genre/
// description map to "" / a non-nil empty slice — never a nil-pointer panic
// and never a "null" genres array on the wire.
func TestService_Search_MetadataFieldsNil(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "s1", Name: "Source One", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"s1": {{ID: 1, Title: "No Metadata"}},
		},
	}
	svc := newService(fc)

	got, err := svc.Search(context.Background(), "No Metadata", nil)
	if err != nil {
		t.Fatalf("Search: unexpected error: %v", err)
	}
	if len(got) == 0 || len(got[0].Candidates) == 0 {
		t.Fatal("expected at least one group and candidate")
	}
	c := got[0].Candidates[0]
	if c.Author != "" {
		t.Errorf("Candidate.Author: got %q, want empty", c.Author)
	}
	if c.Artist != "" {
		t.Errorf("Candidate.Artist: got %q, want empty", c.Artist)
	}
	if c.Description != "" {
		t.Errorf("Candidate.Description: got %q, want empty", c.Description)
	}
	if c.Genres == nil {
		t.Error("Candidate.Genres: got nil, want non-nil empty slice (JSON must be [] not null)")
	}
	if len(c.Genres) != 0 {
		t.Errorf("Candidate.Genres: got %v, want empty", c.Genres)
	}
}

// --- InspectChapters tests ---------------------------------------------------

// TestService_InspectChapters verifies that InspectChapters maps FetchChapters
// to []ChapterInspectDTO with the correct number and name.
func TestService_InspectChapters(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		chapters: []suwayomi.Chapter{
			{ID: 1, Name: "Chapter 1", Number: ptrF64(1.0)},
			{ID: 2, Name: "Chapter 2", Number: ptrF64(2.0)},
			{ID: 3, Name: "Special", Number: nil},
		},
	}
	svc := newService(fc)

	got, err := svc.InspectChapters(context.Background(), "src", 7)
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

	// Chapter 3 — nil number
	if got[2].Number != nil {
		t.Errorf("InspectChapters[2].Number: got %v, want nil", got[2].Number)
	}
	if got[2].Name != "Special" {
		t.Errorf("InspectChapters[2].Name: got %q, want %q", got[2].Name, "Special")
	}
}

// TestService_InspectChapters_Error verifies that a FetchChapters error is
// propagated to the caller.
func TestService_InspectChapters_Error(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("suwayomi: manga not found")
	fc := &fakeClient{chaptersErr: sentinel}
	svc := newService(fc)

	_, err := svc.InspectChapters(context.Background(), "src", 99)
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
		sources: []suwayomi.Source{
			{ID: "src-a", Name: "Alpha Source", Lang: "en"},
		},
		browseResults: map[suwayomi.BrowseType]suwayomi.BrowseResult{
			suwayomi.BrowsePopular: {
				Mangas: []suwayomi.Manga{
					{ID: 1, Title: "Solo Leveling", URL: "/manga/1", ThumbnailURL: ptrStr("http://t/1")},
					{ID: 2, Title: "Omniscient Reader", URL: "/manga/2", ThumbnailURL: nil},
				},
				HasNextPage: true,
			},
		},
	}
	svc := newService(fc)

	got, err := svc.Browse(context.Background(), "src-a", suwayomi.BrowsePopular, 1)
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
	assertCandidateTags(t, c0, "src-a", "Alpha Source", "en")
	if c0.URL != "/manga/1" {
		t.Errorf("Browse candidate[0].URL: got %q, want /manga/1", c0.URL)
	}
	// ThumbnailURL must be Tsundoku's own cover-proxy path, not Suwayomi's raw
	// thumbnail URL (B2 fix).
	const wantProxyPath = "/api/sources/src-a/manga/1/cover"
	if c0.ThumbnailURL != wantProxyPath {
		t.Errorf("Browse candidate[0].ThumbnailURL: got %q, want %q", c0.ThumbnailURL, wantProxyPath)
	}
	// Nil thumbnail → empty string (no proxy path minted with nothing to fetch).
	if got.Manga[1].ThumbnailURL != "" {
		t.Errorf("Browse candidate[1].ThumbnailURL: got %q, want empty", got.Manga[1].ThumbnailURL)
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
		sources: []suwayomi.Source{
			{ID: "src-b", Name: "Beta Source", Lang: "ko"},
		},
		browseResults: map[suwayomi.BrowseType]suwayomi.BrowseResult{
			suwayomi.BrowseLatest: {
				Mangas:      []suwayomi.Manga{{ID: 9, Title: "Tower of God", URL: "/manga/9"}},
				HasNextPage: false,
			},
		},
	}
	svc := newService(fc)

	got, err := svc.Browse(context.Background(), "src-b", suwayomi.BrowseLatest, 3)
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
		sources: []suwayomi.Source{{ID: "real", Name: "Real", Lang: "en"}},
	}
	svc := newService(fc)

	_, err := svc.Browse(context.Background(), "ghost", suwayomi.BrowsePopular, 1)
	if !errors.Is(err, imports.ErrSourceNotFound) {
		t.Errorf("Browse unknown source: err = %v, want ErrSourceNotFound", err)
	}
}

// TestService_Browse_UpstreamError verifies that a client.Browse failure
// propagates verbatim — browse is single-source, so a failure is the whole
// request (no partial-results carve-out).
func TestService_Browse_UpstreamError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("suwayomi: source offline")
	fc := &fakeClient{
		sources:   []suwayomi.Source{{ID: "src-a", Name: "Alpha", Lang: "en"}},
		browseErr: sentinel,
	}
	svc := newService(fc)

	_, err := svc.Browse(context.Background(), "src-a", suwayomi.BrowsePopular, 1)
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

	_, err := svc.Browse(context.Background(), "any", suwayomi.BrowsePopular, 1)
	if !errors.Is(err, sentinel) {
		t.Errorf("Browse sources error: err = %v, want to wrap %v", err, sentinel)
	}
}

// TestService_Search_URLPopulated is the non-vacuous proof that Search now
// surfaces the manga url on each candidate — removing the URL mapping fails it.
func TestService_Search_URLPopulated(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{
		sources: []suwayomi.Source{{ID: "s1", Name: "Source One", Lang: "en"}},
		searchResults: map[string][]suwayomi.Manga{
			"s1": {{ID: 42, Title: "Attack on Titan", URL: "/manga/42"}},
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
	ingest := suwayomi.NewIngest(fc, db)
	return imports.NewService(fc, ingest, db, "")
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
// providers with DIFFERENT per-source titles ("Solo Leveling" / "Solo Leveling
// (Official)") are adopted under one canonical title "Solo Leveling" → exactly
// ONE Series row (slug = disk.Slugify("Solo Leveling")), TWO SeriesProvider rows
// with correct importances, and chapters in state wanted.
func TestService_Adopt_TwoProviders(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Solo Leveling"
		srcA           = "mangadex"
		mangaIDA       = 101
		impA           = 10 // higher importance → ranked first
		srcB           = "toonily"
		mangaIDB       = 202
		impB           = 5
	)

	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			mangaIDA: makeAdoptChapters(1000, 2),
			mangaIDB: makeAdoptChapters(2000, 3),
		},
	}
	ingest := suwayomi.NewIngest(fc, db)
	svc := imports.NewService(fc, ingest, db, "")

	id, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: srcA, MangaID: mangaIDA, Importance: impA},
			{Source: srcB, MangaID: mangaIDB, Importance: impB},
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

// TestService_Adopt_Idempotent verifies that calling Adopt twice with the same
// request produces no new Series/SeriesProvider/Chapter rows on the second call.
func TestService_Adopt_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Tower of God"
		srcA           = "webtoons"
		mangaIDA       = 301
		impA           = 10
	)

	chapA := makeAdoptChapters(3000, 2)
	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			mangaIDA: chapA,
		},
	}
	ingest := suwayomi.NewIngest(fc, db)
	svc := imports.NewService(fc, ingest, db, "")

	req := imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: srcA, MangaID: mangaIDA, Importance: impA},
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
		srcA           = "mangaplus"
		mangaIDA       = 401
		impA           = 10
		srcB           = "mangasee"
		mangaIDB       = 402
		impB           = 5
	)

	chapA := makeAdoptChapters(4000, 2)
	chapB := makeAdoptChapters(5000, 1)
	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			mangaIDA: chapA,
			mangaIDB: chapB,
		},
	}
	ingest := suwayomi.NewIngest(fc, db)
	svc := imports.NewService(fc, ingest, db, "")

	// First adopt: one provider.
	if _, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title:     canonicalTitle,
		Providers: []imports.AdoptProvider{{Source: srcA, MangaID: mangaIDA, Importance: impA}},
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
		Providers: []imports.AdoptProvider{{Source: srcB, MangaID: mangaIDB, Importance: impB}},
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

	t.Run("set_category", func(t *testing.T) {
		db := testdb.New(t)
		fc := &fakeClient{
			chaptersPerManga: map[int][]suwayomi.Chapter{
				501: makeAdoptChapters(6000, 1),
			},
		}
		ingest := suwayomi.NewIngest(fc, db)
		svc := imports.NewService(fc, ingest, db, "")

		_, err := svc.Adopt(ctx, imports.AdoptRequest{
			Title:    "Berserk",
			Category: "Manga",
			Providers: []imports.AdoptProvider{
				{Source: "mangadex", MangaID: 501, Importance: 1},
			},
		})
		if err != nil {
			t.Fatalf("Adopt with category: %v", err)
		}

		s := db.Series.Query().OnlyX(ctx)
		if name := s.QueryCategory().OnlyX(ctx).Name; name != "Manga" {
			t.Errorf("Series category: got %q, want Manga", name)
		}
	})

	t.Run("default_category", func(t *testing.T) {
		db := testdb.New(t)
		fc := &fakeClient{
			chaptersPerManga: map[int][]suwayomi.Chapter{
				502: makeAdoptChapters(7000, 1),
			},
		}
		ingest := suwayomi.NewIngest(fc, db)
		svc := imports.NewService(fc, ingest, db, "")

		_, err := svc.Adopt(ctx, imports.AdoptRequest{
			Title:    "Naruto",
			Category: "", // omitted — should default to Other
			Providers: []imports.AdoptProvider{
				{Source: "mangasee", MangaID: 502, Importance: 1},
			},
		})
		if err != nil {
			t.Fatalf("Adopt without category: %v", err)
		}

		s := db.Series.Query().OnlyX(ctx)
		if name := s.QueryCategory().OnlyX(ctx).Name; name != "Other" {
			t.Errorf("Series category: got %q, want Other", name)
		}
	})
}

// TestService_Adopt_NoSilentPartial verifies §16: when one provider's AddSeries
// errors mid-group, Adopt returns a non-nil error naming the source(s) already
// attached in this call. The successful provider's rows ARE present (no rollback).
func TestService_Adopt_NoSilentPartial(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle = "Demon Slayer"
		srcOK          = "mangadex"
		mangaIDOK      = 601
		impOK          = 10
		srcFail        = "toonily"
		mangaIDFail    = 602
		impFail        = 5
	)

	injectErr := errors.New("suwayomi: source unavailable")
	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			mangaIDOK: makeAdoptChapters(8000, 2),
		},
		chapterErrs: map[int]error{
			mangaIDFail: injectErr,
		},
	}
	ingest := suwayomi.NewIngest(fc, db)
	svc := imports.NewService(fc, ingest, db, "")

	req := imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: srcOK, MangaID: mangaIDOK, Importance: impOK},
			{Source: srcFail, MangaID: mangaIDFail, Importance: impFail},
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

	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			701: makeAdoptChapters(9000, 1),
		},
	}
	svc := newServiceDB(t, fc)

	_, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: "One Piece",
		// Categories are user-defined; "invalid" now means filesystem-unsafe (it
		// becomes a folder name), not "not in a fixed enum".
		Category: "bad/name",
		Providers: []imports.AdoptProvider{
			{Source: "mangadex", MangaID: 701, Importance: 1},
		},
	})
	if err == nil {
		t.Fatal("Adopt: expected error for invalid category, got nil")
	}
}
