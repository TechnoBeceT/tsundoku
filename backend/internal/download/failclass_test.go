package download

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// wrappedImageFetch mimics the error stagePages produces for a transient per-image
// failure that survived the retries: ErrImageFetch wrapping the underlying cause.
func wrappedImageFetch(underlying string) error {
	return fmt.Errorf("sourceengine fetcher: image: %w: %v", sourceengine.ErrImageFetch, errors.New(underlying))
}

// TestIsChapterSpecificFailure_ErrImageFetch proves a surviving per-image fetch
// failure (ErrImageFetch) is classified CHAPTER-SPECIFIC even though its underlying
// cause (a 502 server_error) is a source-wide errorclass category — the sentinel
// override is what keeps one flaky page off the source breaker.
func TestIsChapterSpecificFailure_ErrImageFetch(t *testing.T) {
	err := wrappedImageFetch("502 bad gateway")
	if !isChapterSpecificFailure(err) {
		t.Errorf("isChapterSpecificFailure(ErrImageFetch) = false, want true (a flaky page is chapter-specific)")
	}
	// The whole point: shouldRecordGateFailure must be false so the breaker is not
	// tripped, while the caller still BUMPS the per-source budget (chapter-specific).
	if shouldRecordGateFailure(context.Background(), err) {
		t.Errorf("shouldRecordGateFailure(ErrImageFetch) = true, want false (one flaky page must not trip the source breaker)")
	}
}

// TestShouldRecordGateFailure_BanImageStaysSourceWide proves a ban-class image
// error (captcha / rate_limit) — which stagePages leaves UNWRAPPED — stays
// source-wide and DOES record a breaker failure, so a genuine block still pauses the
// whole source.
func TestShouldRecordGateFailure_BanImageStaysSourceWide(t *testing.T) {
	for _, msg := range []string{"sourceengine fetcher: image: cloudflare challenge", "sourceengine fetcher: image: 429 too many requests"} {
		err := errors.New(msg)
		if isChapterSpecificFailure(err) {
			t.Errorf("isChapterSpecificFailure(%q) = true, want false (a ban is source-wide)", msg)
		}
		if !shouldRecordGateFailure(context.Background(), err) {
			t.Errorf("shouldRecordGateFailure(%q) = false, want true (a ban must trip the breaker)", msg)
		}
	}
}

// TestShouldRecordGateFailure_PagesErrorStillTripsBreaker proves the ban-detection
// carve-out: a page-RESOLUTION (Client.Pages) source-wide failure is NOT wrapped in
// ErrImageFetch, stays source-wide, and still records a breaker failure — a real ban
// at the session stage blocks the whole source exactly as before.
func TestShouldRecordGateFailure_PagesErrorStillTripsBreaker(t *testing.T) {
	err := errors.New("sourceengine fetcher: pages: 502 bad gateway")
	if isChapterSpecificFailure(err) {
		t.Errorf("isChapterSpecificFailure(pages error) = true, want false (a page-resolution failure is source-wide)")
	}
	if !shouldRecordGateFailure(context.Background(), err) {
		t.Errorf("shouldRecordGateFailure(pages error) = false, want true (ban detection at the session stage must be preserved)")
	}
}
