package sourceengine

import (
	"context"
	"errors"
	"time"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
)

// imageRetries is how many EXTRA times a single page's byte fetch is retried
// after its first attempt when the failure is TRANSIENT (a one-off 502 / dropped
// connection / slow-page timeout). Total attempts per page are therefore
// 1 + imageRetries. Owner-picked value — do not change.
const imageRetries = 3

// imageRetryBackoff is the flat pause between successive transient-image retries.
// Owner-picked value — do not change.
const imageRetryBackoff = 500 * time.Millisecond

// ErrImageFetch marks a per-image byte-fetch failure that survived the transient
// retries in stagePages. It exists so the download dispatcher classifies such a
// failure as CHAPTER-SPECIFIC (download.isChapterSpecificFailure errors.Is-es it):
// reaching the image-fetch stage PROVES the source session is alive — the
// chapter-list refresh and page-resolution (Client.Pages) already succeeded this
// attempt — so a persistent single-image failure is about THIS page/chapter, not
// the source being down. Consequence: the per-(chapter,source) budget is charged
// (attempts++, still exhausting to permanently_failed at max) but the source
// circuit-breaker is NOT tripped, so one flaky page can never pause a healthy
// source.
//
// It wraps ONLY the per-image Client.Image failure path (see stagePages), NEVER
// the earlier session stages: a Client.Pages / chapter-list / search failure stays
// SOURCE-WIDE and still trips the breaker (that is where a real ban blocks the
// whole session). It is also applied ONLY to a TRANSIENT surviving error — a
// ban-class image failure (captcha / rate_limit) stays source-wide (see
// isTransientImageError).
var ErrImageFetch = errors.New("sourceengine: image fetch failed after retries")

// isTransientImageError reports whether a Client.Image failure is worth an
// immediate re-hit: a timeout / network / server_error is a one-off blip the same
// request may clear on retry. Everything else falls straight through WITHOUT a
// retry:
//   - a chapter-specific error (not_found / parse) or a broken/validation failure
//     won't fix on retry — the page/chapter itself is broken;
//   - a ban-class error (captcha / rate_limit) must NOT be hammered — an immediate
//     re-hit is actively harmful and the source is genuinely blocking.
//
// The SAME predicate also decides the ErrImageFetch chapter-specific override in
// stagePages: only a transient survivor is reclassified off the breaker; a
// ban-class image failure stays source-wide.
func isTransientImageError(err error) bool {
	switch errorclass.Classify(err) {
	case errorclass.CategoryTimeout, errorclass.CategoryNetwork, errorclass.CategoryServerError:
		return true
	default:
		return false
	}
}

// fetchImageRetrying downloads one page's raw bytes via Client.Image, retrying a
// TRANSIENT failure (see isTransientImageError) up to imageRetries times with an
// imageRetryBackoff pause between tries. A non-transient failure (chapter-specific
// or ban-class) is returned on the FIRST attempt without a retry. The backoff wait
// is abandoned the instant ctx is cancelled/expired (returning ctx.Err()), so a
// graceful shutdown is never delayed by a pending retry. The returned error is the
// raw Client.Image error (unwrapped) — the caller decides how to classify/wrap it.
func (f *Fetcher) fetchImageRetrying(ctx context.Context, sourceID int64, link fetcher.PageLink) ([]byte, string, error) {
	var (
		data        []byte
		contentType string
		err         error
	)
	for attempt := 0; ; attempt++ {
		data, contentType, err = f.client.Image(ctx, sourceID, link.URL, link.ImageURL)
		if err == nil {
			return data, contentType, nil
		}
		if attempt >= imageRetries || !isTransientImageError(err) {
			return nil, "", err
		}
		// Flat backoff before the next try, but never outlast a cancelled context.
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(imageRetryBackoff):
		}
	}
}
