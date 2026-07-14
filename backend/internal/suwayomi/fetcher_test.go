// Package suwayomi_test — unit tests for Fetcher.
//
// All tests use an in-process stub Client; no Java, no network, no Suwayomi
// binary is required. The stub is defined in this file and implements exactly
// the two Client methods that Fetcher calls (ChapterPages, PageBytes); the
// remaining methods embed a nil-panic guard via a struct embedding.
package suwayomi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// --- stub Client -------------------------------------------------------------

// stubClient is an in-process fake that implements suwayomi.Client.
// Only ChapterPages and PageBytes are wired; the other methods are stubs that
// panic if called (they must never be reached by Fetcher).
type stubClient struct {
	// pages is the ordered list of page URLs ChapterPages returns.
	pages []string
	// pagesErr is the error ChapterPages returns (nil = success).
	pagesErr error
	// pageData maps page URL → (data, ext) returned by PageBytes.
	pageData map[string]stubPage
	// pageBytesErr maps page URL → error to return from PageBytes.
	pageBytesErr map[string]error
}

type stubPage struct {
	data []byte
	ext  string
}

func (s *stubClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	if s.pagesErr != nil {
		return nil, s.pagesErr
	}
	return s.pages, nil
}

func (s *stubClient) PageBytes(_ context.Context, pageURL string) ([]byte, string, error) {
	if err, ok := s.pageBytesErr[pageURL]; ok && err != nil {
		return nil, "", err
	}
	if p, ok := s.pageData[pageURL]; ok {
		return p.data, p.ext, nil
	}
	panic("stubClient.PageBytes: unexpected URL: " + pageURL)
}

func (s *stubClient) ServerSettings(_ context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (s *stubClient) SetServerSettings(_ context.Context, _ suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (s *stubClient) Extensions(_ context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (s *stubClient) SetExtensionState(_ context.Context, _ string, _ suwayomi.ExtensionAction) error {
	return nil
}
func (s *stubClient) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (s *stubClient) ExtensionRepos(_ context.Context) ([]string, error)    { return nil, nil }
func (s *stubClient) SetExtensionRepos(_ context.Context, _ []string) error { return nil }
func (s *stubClient) SourcePreferences(_ context.Context, _ string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (s *stubClient) SetSourcePreference(_ context.Context, _ string, _ int, _ suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (s *stubClient) ExtensionSources(_ context.Context, _ string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (s *stubClient) SetSourceEnabled(_ context.Context, _ string, _ bool) error { return nil }

// The remaining Client methods are unused by Fetcher; they panic loudly if
// reached so a future code-change that calls them is caught immediately.
func (s *stubClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("stubClient.Sources: must not be called by Fetcher")
}
func (s *stubClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	panic("stubClient.Search: must not be called by Fetcher")
}
func (s *stubClient) Browse(_ context.Context, _ string, _ suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	panic("stubClient.Browse: must not be called by Fetcher")
}
func (s *stubClient) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("stubClient.FetchChapters: must not be called by Fetcher")
}
func (s *stubClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("stubClient.MangaChapters: must not be called by Fetcher")
}
func (s *stubClient) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("stubClient.MangaMeta: must not be called by Fetcher")
}
func (s *stubClient) FetchMangaDetails(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("stubClient.FetchMangaDetails: must not be called by Fetcher")
}

// --- helpers -----------------------------------------------------------------

// makeRef returns a minimal FetchRef with the given Suwayomi chapter ID.
func makeRef(suwayomiID int) fetcher.FetchRef {
	return fetcher.FetchRef{
		Provider:   "test-provider",
		SuwayomiID: suwayomiID,
	}
}

// --- tests -------------------------------------------------------------------

// TestFetcher_HappyPath verifies that Fetch assembles the correct ChapterPages
// when the client returns N page URLs, each of which yields distinct bytes and
// extensions. The assertion is non-vacuous: it checks the exact bytes and
// extension for every page in order.
func TestFetcher_HappyPath(t *testing.T) {
	t.Parallel()

	jpg, png, wbp := validJPEG(t), validPNG(t), validWebP(t)
	urls := []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"}
	data := map[string]stubPage{
		"http://sw/page/0": {data: jpg, ext: "jpg"},
		"http://sw/page/1": {data: png, ext: "png"},
		"http://sw/page/2": {data: wbp, ext: "webp"},
	}
	client := &stubClient{pages: urls, pageData: data}

	f := suwayomi.NewFetcher(client)
	got, err := f.Fetch(context.Background(), makeRef(42))
	if err != nil {
		t.Fatalf("Fetch: unexpected error: %v", err)
	}
	if got.PageCount != 3 {
		t.Errorf("PageCount: got %d, want 3", got.PageCount)
	}
	if len(got.Pages) != 3 {
		t.Fatalf("len(Pages): got %d, want 3", len(got.Pages))
	}

	want := []fetcher.PageImage{
		{Data: jpg, Ext: "jpg"},
		{Data: png, Ext: "png"},
		{Data: wbp, Ext: "webp"},
	}
	for i, w := range want {
		got := got.Pages[i]
		if got.Ext != w.Ext {
			t.Errorf("Pages[%d].Ext: got %q, want %q", i, got.Ext, w.Ext)
		}
		if string(got.Data) != string(w.Data) {
			t.Errorf("Pages[%d].Data: mismatch", i)
		}
	}
}

// TestFetcher_EmitsPerPageProgress verifies that Fetch drives the context-carried
// progress sink once after each successfully fetched page, with (current, total)
// running (1,N)..(N,N). This is the signal that powers the live download bar.
func TestFetcher_EmitsPerPageProgress(t *testing.T) {
	t.Parallel()

	urls := []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"}
	data := map[string]stubPage{
		"http://sw/page/0": {data: validJPEG(t), ext: "jpg"},
		"http://sw/page/1": {data: validPNG(t), ext: "png"},
		"http://sw/page/2": {data: validWebP(t), ext: "webp"},
	}
	client := &stubClient{pages: urls, pageData: data}

	type call struct{ current, total int }
	var seen []call
	ctx := fetcher.WithProgress(context.Background(), func(current, total int) {
		seen = append(seen, call{current, total})
	})

	f := suwayomi.NewFetcher(client)
	got, err := f.Fetch(ctx, makeRef(42))
	if err != nil {
		t.Fatalf("Fetch: unexpected error: %v", err)
	}
	if got.PageCount != 3 {
		t.Errorf("PageCount: got %d, want 3", got.PageCount)
	}

	want := []call{{1, 3}, {2, 3}, {3, 3}}
	if len(seen) != len(want) {
		t.Fatalf("progress calls: got %d (%+v), want %d", len(seen), seen, len(want))
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("progress call %d: got %+v, want %+v", i, seen[i], w)
		}
	}
}

// TestFetcher_EmptyPageListEmitsNoProgress verifies that a zero-page chapter (G4)
// fails WITHOUT emitting any progress and without downloading any page — the guard
// fires before the page loop, so the progress sink is never touched.
func TestFetcher_EmptyPageListEmitsNoProgress(t *testing.T) {
	t.Parallel()

	client := &stubClient{pages: []string{}}

	called := false
	ctx := fetcher.WithProgress(context.Background(), func(int, int) { called = true })

	f := suwayomi.NewFetcher(client)
	_, err := f.Fetch(ctx, makeRef(0))
	if !errors.Is(err, suwayomi.ErrNoPages) {
		t.Fatalf("Fetch on empty page list: err %v, want ErrNoPages", err)
	}
	if called {
		t.Error("progress sink must not be called for a zero-page chapter")
	}
}

// TestFetcher_ChapterPagesError verifies that a ChapterPages error is propagated
// and the returned ChapterPages is the zero value (no partial success).
func TestFetcher_ChapterPagesError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("suwayomi: chapter not found")
	client := &stubClient{pagesErr: sentinel}

	f := suwayomi.NewFetcher(client)
	got, err := f.Fetch(context.Background(), makeRef(7))
	if !errors.Is(err, sentinel) {
		t.Errorf("err: got %v, want to wrap %v", err, sentinel)
	}
	if got.PageCount != 0 || len(got.Pages) != 0 {
		t.Errorf("ChapterPages on error must be zero value, got %+v", got)
	}
}

// TestFetcher_PageBytesError verifies that a PageBytes failure on page k (k>0)
// is propagated and no partial ChapterPages is returned. The caller must never
// silently receive k-1 pages with a nil error.
func TestFetcher_PageBytesError(t *testing.T) {
	t.Parallel()

	urls := []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"}
	sentinel := errors.New("suwayomi: page fetch failed")
	data := map[string]stubPage{
		// Page 0 is a VALID image so the loop reaches page 1, where the fetch error
		// under test occurs (a broken page 0 would fail validation first).
		"http://sw/page/0": {data: validJPEG(t), ext: "jpg"},
	}
	pageBytesErr := map[string]error{
		"http://sw/page/1": sentinel, // fails on the second page (k=1)
	}
	client := &stubClient{pages: urls, pageData: data, pageBytesErr: pageBytesErr}

	f := suwayomi.NewFetcher(client)
	got, err := f.Fetch(context.Background(), makeRef(7))
	if !errors.Is(err, sentinel) {
		t.Errorf("err: got %v, want to wrap %v", err, sentinel)
	}
	// No partial success — zero value must be returned.
	if got.PageCount != 0 || len(got.Pages) != 0 {
		t.Errorf("partial success must not be returned on PageBytes error, got %+v", got)
	}
}

// TestFetcher_EmptyPageList verifies that Fetch on a chapter with zero pages (G4)
// FAILS with ErrNoPages and returns the zero ChapterPages — a zero-page source
// response must retry / fall through, never render a "downloaded" empty CBZ.
func TestFetcher_EmptyPageList(t *testing.T) {
	t.Parallel()

	client := &stubClient{pages: []string{}} // ChapterPages returns an empty list

	f := suwayomi.NewFetcher(client)
	got, err := f.Fetch(context.Background(), makeRef(0))
	if !errors.Is(err, suwayomi.ErrNoPages) {
		t.Fatalf("Fetch on empty page list: err %v, want ErrNoPages", err)
	}
	if got.PageCount != 0 || len(got.Pages) != 0 {
		t.Errorf("ChapterPages on error must be zero value, got %+v", got)
	}
}

// TestFetcher_BrokenPageFailsWholeChapter is the core reliability guarantee: a
// multi-page chapter where a MIDDLE page is broken (truncated, HTML-as-200, or a
// 0-byte body) fails the WHOLE fetch with ErrBrokenPage and returns the zero
// ChapterPages — no partial slice, so the dispatcher never renders a CBZ with a
// missing panel. Each broken shape is exercised with real valid pages around it.
func TestFetcher_BrokenPageFailsWholeChapter(t *testing.T) {
	t.Parallel()

	broken := map[string][]byte{
		"truncated jpeg": truncatedJPEG(t),
		"html as 200":    htmlPage(),
		"empty body":     {},
	}
	for name, badPage := range broken {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			urls := []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"}
			data := map[string]stubPage{
				"http://sw/page/0": {data: validJPEG(t), ext: "jpg"},
				"http://sw/page/1": {data: badPage, ext: "jpg"}, // the broken middle page
				"http://sw/page/2": {data: validPNG(t), ext: "png"},
			}
			client := &stubClient{pages: urls, pageData: data}

			f := suwayomi.NewFetcher(client)
			got, err := f.Fetch(context.Background(), makeRef(11))
			if !errors.Is(err, suwayomi.ErrBrokenPage) {
				t.Fatalf("err %v, want to wrap ErrBrokenPage", err)
			}
			if got.PageCount != 0 || len(got.Pages) != 0 {
				t.Errorf("broken page must yield zero ChapterPages, got %+v", got)
			}
		})
	}
}

// TestFetcher_CancelledContext verifies that a context cancelled before Fetch is
// called returns ctx.Err() (or a wrap of it) before fetching any pages.
func TestFetcher_CancelledContext(t *testing.T) {
	t.Parallel()

	// Pre-cancel the context before calling Fetch.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Provide real page data; Fetch must abort at the initial ctx.Err() check
	// before reaching ChapterPages or the page loop.
	urls := []string{"http://sw/page/0", "http://sw/page/1"}
	data := map[string]stubPage{
		"http://sw/page/0": {data: []byte{0x01}, ext: "jpg"},
		"http://sw/page/1": {data: []byte{0x02}, ext: "png"},
	}
	client := &stubClient{pages: urls, pageData: data}

	f := suwayomi.NewFetcher(client)
	_, err := f.Fetch(ctx, makeRef(5))
	if err == nil {
		t.Fatal("Fetch with cancelled context: expected an error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Fetch with cancelled context: err %v does not wrap context.Canceled", err)
	}
}

// TestFetcher_CancelledContextMidLoop verifies that a context cancelled after
// ChapterPages returns (but before all pages are downloaded) causes Fetch to
// abort mid-loop and return ctx.Err(). This exercises the ctx.Err() check
// inside the page loop — a distinct reachable path from the pre-call check.
func TestFetcher_CancelledContextMidLoop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	// midLoopCancelClient cancels the context after the first PageBytes call so
	// the ctx.Err() check at the top of the next loop iteration catches it. The
	// first page must be a VALID image so it passes validation and the loop reaches
	// the next iteration's ctx check (which is the path under test).
	client := &midLoopCancelClient{
		pages:  []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"},
		cancel: cancel,
		data:   validJPEG(t),
	}

	f := suwayomi.NewFetcher(client)
	_, err := f.Fetch(ctx, makeRef(99))
	if err == nil {
		t.Fatal("Fetch with mid-loop cancel: expected an error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("mid-loop cancel: err %v does not wrap context.Canceled", err)
	}
}

// midLoopCancelClient is a test-only stub that cancels the context after
// returning the first page's bytes, simulating cancellation mid-loop.
type midLoopCancelClient struct {
	pages  []string
	cancel context.CancelFunc
	data   []byte
	calls  int
}

func (m *midLoopCancelClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	return m.pages, nil
}

func (m *midLoopCancelClient) PageBytes(_ context.Context, _ string) ([]byte, string, error) {
	m.calls++
	if m.calls == 1 {
		// First page succeeds, then we cancel — the next iteration's ctx.Err()
		// check inside the loop will catch it.
		m.cancel()
	}
	return m.data, "jpg", nil
}

func (m *midLoopCancelClient) ServerSettings(_ context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (m *midLoopCancelClient) SetServerSettings(_ context.Context, _ suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (m *midLoopCancelClient) Extensions(_ context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (m *midLoopCancelClient) SetExtensionState(_ context.Context, _ string, _ suwayomi.ExtensionAction) error {
	return nil
}
func (m *midLoopCancelClient) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (m *midLoopCancelClient) ExtensionRepos(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *midLoopCancelClient) SetExtensionRepos(_ context.Context, _ []string) error {
	return nil
}
func (m *midLoopCancelClient) SourcePreferences(_ context.Context, _ string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (m *midLoopCancelClient) SetSourcePreference(_ context.Context, _ string, _ int, _ suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (m *midLoopCancelClient) ExtensionSources(_ context.Context, _ string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (m *midLoopCancelClient) SetSourceEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}

func (m *midLoopCancelClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("midLoopCancelClient.Sources: must not be called")
}
func (m *midLoopCancelClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	panic("midLoopCancelClient.Search: must not be called")
}
func (m *midLoopCancelClient) Browse(_ context.Context, _ string, _ suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	panic("midLoopCancelClient.Browse: must not be called")
}
func (m *midLoopCancelClient) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("midLoopCancelClient.FetchChapters: must not be called")
}
func (m *midLoopCancelClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("midLoopCancelClient.MangaChapters: must not be called")
}
func (m *midLoopCancelClient) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("midLoopCancelClient.MangaMeta: must not be called")
}
func (m *midLoopCancelClient) FetchMangaDetails(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("midLoopCancelClient.FetchMangaDetails: must not be called")
}
