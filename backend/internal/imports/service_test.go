// Package imports_test — unit tests for Service (Sources, Search, InspectChapters).
//
// All tests use an in-process fakeClient; no Suwayomi process, no network, no DB.
package imports_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// --- fake client -------------------------------------------------------------

// fakeClient implements suwayomi.Client with canned per-source responses.
// Methods unused by Service return nil, nil.
type fakeClient struct {
	// sources is the slice returned by Sources.
	sources []suwayomi.Source
	// sourcesErr is the error returned by Sources (nil = success).
	sourcesErr error
	// searchResults maps sourceID → results returned by Search.
	searchResults map[string][]suwayomi.Manga
	// searchErrs maps sourceID → error returned by Search (nil = success).
	searchErrs map[string]error
	// chapters is the slice returned by FetchChapters.
	chapters []suwayomi.Chapter
	// chaptersErr is the error returned by FetchChapters (nil = success).
	chaptersErr error
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

func (f *fakeClient) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	return f.chapters, f.chaptersErr
}

// Remaining Client methods are unused by Service in Task 3; return nil, nil.
func (f *fakeClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	return nil, nil
}
func (f *fakeClient) PageBytes(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}

// --- helpers -----------------------------------------------------------------

// ptrStr returns a pointer to s.
func ptrStr(s string) *string { return &s }

// ptrF64 returns a pointer to v.
func ptrF64(v float64) *float64 { return &v }

// newService constructs a Service with a fake client and nil ingest/db (unused in Task 3).
func newService(fc *fakeClient) *imports.Service {
	return imports.NewService(fc, nil, nil, "")
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
func TestService_Search_CandidateFields(t *testing.T) {
	t.Parallel()

	thumb := "http://thumb.test/img.jpg"
	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "s1", Name: "Source One", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"s1": {{ID: 42, Title: "Attack on Titan", ThumbnailURL: ptrStr(thumb)}},
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
	if c.ThumbnailURL != thumb {
		t.Errorf("Candidate.ThumbnailURL: got %q, want %q", c.ThumbnailURL, thumb)
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
