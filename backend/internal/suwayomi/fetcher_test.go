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

// The remaining Client methods are unused by Fetcher; they panic loudly if
// reached so a future code-change that calls them is caught immediately.
func (s *stubClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("stubClient.Sources: must not be called by Fetcher")
}
func (s *stubClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	panic("stubClient.Search: must not be called by Fetcher")
}
func (s *stubClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("stubClient.MangaChapters: must not be called by Fetcher")
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

	urls := []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"}
	data := map[string]stubPage{
		"http://sw/page/0": {data: []byte{0xAA, 0xBB}, ext: "jpg"},
		"http://sw/page/1": {data: []byte{0xCC, 0xDD}, ext: "png"},
		"http://sw/page/2": {data: []byte{0xEE, 0xFF}, ext: "webp"},
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
		{Data: []byte{0xAA, 0xBB}, Ext: "jpg"},
		{Data: []byte{0xCC, 0xDD}, Ext: "png"},
		{Data: []byte{0xEE, 0xFF}, Ext: "webp"},
	}
	for i, w := range want {
		got := got.Pages[i]
		if got.Ext != w.Ext {
			t.Errorf("Pages[%d].Ext: got %q, want %q", i, got.Ext, w.Ext)
		}
		if string(got.Data) != string(w.Data) {
			t.Errorf("Pages[%d].Data: got %v, want %v", i, got.Data, w.Data)
		}
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
		"http://sw/page/0": {data: []byte{0x01}, ext: "jpg"},
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

// TestFetcher_EmptyPageList verifies that Fetch on a chapter with zero pages
// returns an empty ChapterPages (PageCount==0, Pages==nil-or-empty) with a nil
// error. Zero pages is treated as a valid (if unusual) server response — the
// caller decides whether to retry or skip.
func TestFetcher_EmptyPageList(t *testing.T) {
	t.Parallel()

	client := &stubClient{pages: []string{}} // ChapterPages returns an empty list

	f := suwayomi.NewFetcher(client)
	got, err := f.Fetch(context.Background(), makeRef(0))
	if err != nil {
		t.Fatalf("Fetch on empty page list: unexpected error: %v", err)
	}
	if got.PageCount != 0 {
		t.Errorf("PageCount: got %d, want 0", got.PageCount)
	}
	if len(got.Pages) != 0 {
		t.Errorf("len(Pages): got %d, want 0", len(got.Pages))
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
	// the ctx.Err() check at the top of the next loop iteration catches it.
	client := &midLoopCancelClient{
		pages:  []string{"http://sw/page/0", "http://sw/page/1", "http://sw/page/2"},
		cancel: cancel,
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
	return []byte{0xFF}, "jpg", nil
}

func (m *midLoopCancelClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("midLoopCancelClient.Sources: must not be called")
}
func (m *midLoopCancelClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	panic("midLoopCancelClient.Search: must not be called")
}
func (m *midLoopCancelClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("midLoopCancelClient.MangaChapters: must not be called")
}
