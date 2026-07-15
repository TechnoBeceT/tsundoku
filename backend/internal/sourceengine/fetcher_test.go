package sourceengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestFetcher_Fetch_Success proves Fetch parses ref.Provider as the numeric
// sourceID, calls Pages then Image per page, and assembles ChapterPages in
// page order with the extension derived from each page's content type.
func TestFetcher_Fetch_Success(t *testing.T) {
	pages := []sourceengine.Page{
		{Index: 0, URL: "/ch/1/page/0", ImageURL: "https://x/p0.jpg"},
		{Index: 1, URL: "/ch/1/page/1", ImageURL: "https://x/p1.png"},
	}
	client := fake.New(
		fake.WithPages(7, "/ch/1", pages),
		fake.WithImage(7, "/ch/1/page/0", []byte{1, 2, 3}, "image/jpeg"),
		fake.WithImage(7, "/ch/1/page/1", []byte{4, 5, 6}, "image/png"),
	)
	f := sourceengine.NewFetcher(client)

	var progressCalls [][2]int
	ctx := fetcher.WithProgress(context.Background(), func(current, total int) {
		progressCalls = append(progressCalls, [2]int{current, total})
	})

	got, err := f.Fetch(ctx, fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.PageCount != 2 || len(got.Pages) != 2 {
		t.Fatalf("Fetch PageCount/len(Pages) = %d/%d, want 2/2", got.PageCount, len(got.Pages))
	}
	assertPageImage(t, got.Pages[0], "jpg", []byte{1, 2, 3})
	assertPageImage(t, got.Pages[1], "png", []byte{4, 5, 6})
	assertProgressCalls(t, progressCalls, [][2]int{{1, 2}, {2, 2}})
	if client.CallCount("Pages") != 1 {
		t.Errorf("Pages called %d times, want 1", client.CallCount("Pages"))
	}
	if client.CallCount("Image") != 2 {
		t.Errorf("Image called %d times, want 2", client.CallCount("Image"))
	}
}

// assertPageImage is a shared test helper asserting one fetcher.PageImage's
// extension and raw bytes.
func assertPageImage(t *testing.T, got fetcher.PageImage, wantExt string, wantData []byte) {
	t.Helper()
	if got.Ext != wantExt {
		t.Errorf("PageImage.Ext = %q, want %q", got.Ext, wantExt)
	}
	if string(got.Data) != string(wantData) {
		t.Errorf("PageImage.Data = %v, want %v", got.Data, wantData)
	}
}

// assertProgressCalls is a shared test helper asserting the exact sequence
// of (current,total) pairs a progress sink recorded.
func assertProgressCalls(t *testing.T, got, want [][2]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("progress calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("progress call %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestFetcher_Fetch_NonNumericProvider proves a disk-origin provider (a
// non-numeric ref.Provider, which has no live source) fails fast with no
// client calls at all.
func TestFetcher_Fetch_NonNumericProvider(t *testing.T) {
	client := fake.New()
	f := sourceengine.NewFetcher(client)

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "disk-import", URL: "/ch/1"})
	if err == nil {
		t.Fatal("Fetch: want error for non-numeric provider, got nil")
	}
	if client.CallCount("Pages") != 0 {
		t.Errorf("Pages called %d times, want 0 (must fail before any client call)", client.CallCount("Pages"))
	}
}

// TestFetcher_Fetch_PagesError proves a Pages failure is propagated and no
// Image call is attempted.
func TestFetcher_Fetch_PagesError(t *testing.T) {
	wantErr := errors.New("boom")
	client := fake.New(fake.WithError("Pages", wantErr))
	f := sourceengine.NewFetcher(client)

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Fetch error = %v, want wrapping %v", err, wantErr)
	}
	if client.CallCount("Image") != 0 {
		t.Errorf("Image called %d times, want 0", client.CallCount("Image"))
	}
}

// TestFetcher_Fetch_NoPages proves a chapter with zero pages fails the whole
// attempt (never renders an empty "downloaded" CBZ).
func TestFetcher_Fetch_NoPages(t *testing.T) {
	client := fake.New(fake.WithPages(7, "/ch/1", nil))
	f := sourceengine.NewFetcher(client)

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if !errors.Is(err, sourceengine.ErrNoPages) {
		t.Fatalf("Fetch error = %v, want wrapping ErrNoPages", err)
	}
}

// TestFetcher_Fetch_ImageError proves an Image failure on any page fails the
// whole chapter — no partial ChapterPages is ever returned.
func TestFetcher_Fetch_ImageError(t *testing.T) {
	wantErr := errors.New("page fetch failed")
	pages := []sourceengine.Page{{Index: 0, URL: "/ch/1/page/0", ImageURL: "https://x/p0.jpg"}}
	client := fake.New(
		fake.WithPages(7, "/ch/1", pages),
		fake.WithError("Image", wantErr),
	)
	f := sourceengine.NewFetcher(client)

	got, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Fetch error = %v, want wrapping %v", err, wantErr)
	}
	if got.PageCount != 0 || len(got.Pages) != 0 {
		t.Errorf("Fetch returned a non-empty partial result on failure: %+v", got)
	}
}

// TestFetcher_Fetch_ContextCancelled proves a pre-cancelled context aborts
// before any client call.
func TestFetcher_Fetch_ContextCancelled(t *testing.T) {
	client := fake.New()
	f := sourceengine.NewFetcher(client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.Fetch(ctx, fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil {
		t.Fatal("Fetch: want error for cancelled context, got nil")
	}
	if client.CallCount("Pages") != 0 {
		t.Errorf("Pages called %d times, want 0", client.CallCount("Pages"))
	}
}

// TestFetcher_ExtFromContentType proves the content-type-to-extension mapping
// via a table of pages, one per known type plus one unknown fallback.
func TestFetcher_ExtFromContentType(t *testing.T) {
	tests := []struct {
		contentType string
		wantExt     string
	}{
		{"image/jpeg", "jpg"},
		{"image/png", "png"},
		{"image/webp", "webp"},
		{"image/gif", "gif"},
		{"application/octet-stream", "jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			pages := []sourceengine.Page{{Index: 0, URL: "/p", ImageURL: "https://x/p"}}
			client := fake.New(
				fake.WithPages(7, "/ch/1", pages),
				fake.WithImage(7, "/p", []byte{9}, tt.contentType),
			)
			f := sourceengine.NewFetcher(client)

			got, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got.Pages[0].Ext != tt.wantExt {
				t.Errorf("Ext for %q = %q, want %q", tt.contentType, got.Pages[0].Ext, tt.wantExt)
			}
		})
	}
}

// cancelAfterFirstImage wraps a sourceengine.Client and cancels a captured
// context.CancelFunc right after its FIRST Image call returns — used to
// exercise Fetch's mid-loop ctx.Err() re-check (the second/third/... page of
// a multi-page chapter), which a pre-cancelled context cannot reach.
type cancelAfterFirstImage struct {
	sourceengine.Client
	cancel context.CancelFunc
	calls  int
}

// Image delegates to the wrapped Client, then cancels the context after the
// first call so the NEXT loop iteration's ctx.Err() check fires.
func (w *cancelAfterFirstImage) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	data, contentType, err := w.Client.Image(ctx, sourceID, pageURL, imageURL)
	w.calls++
	if w.calls == 1 {
		w.cancel()
	}
	return data, contentType, err
}

// TestFetcher_Fetch_ContextCancelledMidLoop proves a context cancelled
// BETWEEN pages (not before the call at all) is caught by the loop's
// per-page ctx.Err() re-check and aborts the remaining pages.
func TestFetcher_Fetch_ContextCancelledMidLoop(t *testing.T) {
	pages := []sourceengine.Page{
		{Index: 0, URL: "/ch/1/page/0"},
		{Index: 1, URL: "/ch/1/page/1"},
	}
	base := fake.New(
		fake.WithPages(7, "/ch/1", pages),
		fake.WithImage(7, "/ch/1/page/0", []byte{1}, "image/jpeg"),
		fake.WithImage(7, "/ch/1/page/1", []byte{2}, "image/jpeg"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrapped := &cancelAfterFirstImage{Client: base, cancel: cancel}
	f := sourceengine.NewFetcher(wrapped)

	_, err := f.Fetch(ctx, fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil {
		t.Fatal("Fetch: want error after a mid-loop cancellation, got nil")
	}
	if wrapped.calls != 1 {
		t.Errorf("Image called %d times, want exactly 1 (the loop must abort before the second page)", wrapped.calls)
	}
}

// TestFetcher_ImplementsChapterFetcher is a compile-time-adjacent check that
// exercises the interface value, proving Fetcher satisfies
// fetcher.ChapterFetcher at the call site the download dispatcher will use.
func TestFetcher_ImplementsChapterFetcher(t *testing.T) {
	var _ fetcher.ChapterFetcher = sourceengine.NewFetcher(fake.New())
}
