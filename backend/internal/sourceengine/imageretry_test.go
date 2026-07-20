package sourceengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// flakyImageClient wraps a fake.Client, failing the first failCount Image calls
// with failErr before delegating to the embedded client for the rest. imageCalls
// records every Image call so a test can assert exactly how many attempts the
// per-image retry loop made. Every other Client method is inherited unchanged from
// the embedded fake.
type flakyImageClient struct {
	*fake.Client
	failErr    error
	failCount  int
	imageCalls int
}

// Image fails the first failCount times, then delegates to the embedded fake so a
// seeded WithImage entry is returned once the transient failures are exhausted.
func (c *flakyImageClient) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	c.imageCalls++
	if c.imageCalls <= c.failCount {
		return nil, "", c.failErr
	}
	return c.Client.Image(ctx, sourceID, pageURL, imageURL)
}

// TestStagePages_TransientImage_SucceedsOnRetry proves a transient per-image error
// (a server_error 502) is retried in place and the chapter completes once the page
// finally succeeds — the client is called once per failure plus once for the
// success, all within a single Fetch.
func TestStagePages_TransientImage_SucceedsOnRetry(t *testing.T) {
	jpg := validJPEG(t)
	inner := fake.New(
		fake.WithPages(7, "/ch/1", []sourceengine.Page{{Index: 0, URL: "/ch/1/page/0", ImageURL: "https://x/p0.jpg"}}),
		fake.WithImage(7, "/ch/1/page/0", jpg, "image/jpeg"),
	)
	// Fail twice (transient), succeed on the third call — within the 3-retry budget.
	client := &flakyImageClient{Client: inner, failErr: errors.New("502 bad gateway"), failCount: 2}
	f := sourceengine.NewFetcher(client, t.TempDir())

	got, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err != nil {
		t.Fatalf("Fetch: %v (a transient image error must be retried, not fail the chapter)", err)
	}
	if got.PageCount != 1 || len(got.Pages) != 1 {
		t.Fatalf("PageCount/len(Pages) = %d/%d, want 1/1", got.PageCount, len(got.Pages))
	}
	if client.imageCalls != 3 {
		t.Errorf("Image called %d times, want 3 (2 transient failures + 1 success, ≤3 retries)", client.imageCalls)
	}
}

// TestStagePages_TransientImage_ExhaustsRetries_WrapsErrImageFetch proves a
// PERSISTENT transient image error fails the chapter after exactly 1+imageRetries
// attempts, and the returned error wraps ErrImageFetch (so the dispatcher treats it
// as chapter-specific and never trips the source breaker).
func TestStagePages_TransientImage_ExhaustsRetries_WrapsErrImageFetch(t *testing.T) {
	inner := fake.New(
		fake.WithPages(7, "/ch/1", []sourceengine.Page{{Index: 0, URL: "/ch/1/page/0"}}),
	)
	// failCount larger than the retry budget ⇒ every attempt fails.
	client := &flakyImageClient{Client: inner, failErr: errors.New("502 bad gateway"), failCount: 99}
	f := sourceengine.NewFetcher(client, t.TempDir())

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil {
		t.Fatal("Fetch: want error after retries exhausted, got nil")
	}
	if !errors.Is(err, sourceengine.ErrImageFetch) {
		t.Errorf("err %v does not wrap ErrImageFetch — the dispatcher would trip the breaker on a flaky page", err)
	}
	if client.imageCalls != 4 {
		t.Errorf("Image called %d times, want 4 (1 initial + 3 retries)", client.imageCalls)
	}
}

// TestStagePages_BanImage_NotRetried_StaysSourceWide proves a ban-class per-image
// error (captcha / rate_limit) is NOT retried (a single call, no hammering) and is
// NOT wrapped in ErrImageFetch — so it stays source-wide and still trips the
// breaker downstream.
func TestStagePages_BanImage_NotRetried_StaysSourceWide(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{"captcha", "cloudflare challenge detected"},
		{"rate_limit", "429 too many requests"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := fake.New(
				fake.WithPages(7, "/ch/1", []sourceengine.Page{{Index: 0, URL: "/ch/1/page/0"}}),
				fake.WithError("Image", errors.New(tc.errMsg)),
			)
			f := sourceengine.NewFetcher(inner, t.TempDir())

			_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
			if err == nil {
				t.Fatal("Fetch: want error, got nil")
			}
			if errors.Is(err, sourceengine.ErrImageFetch) {
				t.Errorf("a %s image error must NOT be wrapped in ErrImageFetch (it stays source-wide → breaker): %v", tc.name, err)
			}
			if n := inner.CallCount("Image"); n != 1 {
				t.Errorf("Image called %d times, want 1 (a ban must never be hammered by retries)", n)
			}
		})
	}
}

// TestStagePages_ChapterSpecificImage_NotRetried proves a chapter-specific per-image
// error (not_found — the page/chapter is genuinely broken on this source) is not
// retried and is not wrapped in ErrImageFetch: errorclass already classifies it
// chapter-specific, and a retry would not fix it.
func TestStagePages_ChapterSpecificImage_NotRetried(t *testing.T) {
	inner := fake.New(
		fake.WithPages(7, "/ch/1", []sourceengine.Page{{Index: 0, URL: "/ch/1/page/0"}}),
		fake.WithError("Image", errors.New("404 not found")),
	)
	f := sourceengine.NewFetcher(inner, t.TempDir())

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil {
		t.Fatal("Fetch: want error, got nil")
	}
	if errors.Is(err, sourceengine.ErrImageFetch) {
		t.Errorf("a not_found image error must not be wrapped in ErrImageFetch (it is already chapter-specific): %v", err)
	}
	if n := inner.CallCount("Image"); n != 1 {
		t.Errorf("Image called %d times, want 1 (a chapter-specific error won't fix on retry)", n)
	}
}

// TestStagePages_BrokenImage_NotRetried proves a page that resolves but fails image
// VALIDATION (a truncated / non-image body → ErrBrokenPage) is not retried: the
// bytes arrived, they are just not a valid image, so re-fetching the same page is
// pointless. It stays chapter-specific via ErrBrokenPage, distinct from ErrImageFetch.
func TestStagePages_BrokenImage_NotRetried(t *testing.T) {
	inner := fake.New(
		fake.WithPages(7, "/ch/1", []sourceengine.Page{{Index: 0, URL: "/ch/1/page/0"}}),
		fake.WithImage(7, "/ch/1/page/0", []byte("not-an-image"), "image/jpeg"),
	)
	f := sourceengine.NewFetcher(inner, t.TempDir())

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if !errors.Is(err, sourceengine.ErrBrokenPage) {
		t.Fatalf("err %v does not wrap ErrBrokenPage", err)
	}
	if errors.Is(err, sourceengine.ErrImageFetch) {
		t.Errorf("a broken-page validation failure must not be wrapped in ErrImageFetch: %v", err)
	}
	if n := inner.CallCount("Image"); n != 1 {
		t.Errorf("Image called %d times, want 1 (a validation failure won't fix on retry)", n)
	}
}

// TestResolveLinks_PagesError_NotImageFetch proves a page-RESOLUTION failure
// (Client.Pages — an EARLIER session stage where a real ban blocks the whole
// source) is NOT wrapped in ErrImageFetch, so it stays source-wide and still trips
// the breaker. This guards the ban-detection carve-out: the image-stage exclusion
// must apply ONLY to the per-image fetch, never to page resolution.
func TestResolveLinks_PagesError_NotImageFetch(t *testing.T) {
	inner := fake.New(fake.WithError("Pages", errors.New("502 bad gateway")))
	f := sourceengine.NewFetcher(inner, t.TempDir())

	_, err := f.Fetch(context.Background(), fetcher.FetchRef{Provider: "7", URL: "/ch/1"})
	if err == nil {
		t.Fatal("Fetch: want error, got nil")
	}
	if errors.Is(err, sourceengine.ErrImageFetch) {
		t.Errorf("a page-resolution (Pages) failure must NOT be wrapped in ErrImageFetch — ban detection at the session stage must be preserved: %v", err)
	}
	if inner.CallCount("Image") != 0 {
		t.Errorf("Image called %d times, want 0 (a Pages failure fails before any image fetch)", inner.CallCount("Image"))
	}
}
