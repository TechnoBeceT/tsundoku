// Package sourceengine — this file provides Fetcher, the engine-host-backed
// implementation of the fetcher.ChapterFetcher port (see
// internal/fetcher/fetcher.go). It replaces internal/suwayomi.Fetcher in the
// P2 migration: same shape, same error semantics, different transport.
package sourceengine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// Compile-time assertion: Fetcher must satisfy the ChapterFetcher port.
var _ fetcher.ChapterFetcher = (*Fetcher)(nil)

// ErrNoPages is returned when a source reports a chapter with zero pages. It
// is a fetch failure — a zero-page chapter must retry / fall through to
// another source, never render a "downloaded" empty CBZ.
var ErrNoPages = errors.New("sourceengine: chapter has no pages")

// Fetcher implements fetcher.ChapterFetcher by delegating to a Client. It
// converts a fetcher.FetchRef into decoded page images by calling
// Client.Pages to obtain the ordered page list, then Client.Image for each
// page to download the raw bytes.
type Fetcher struct {
	client Client
}

// NewFetcher constructs a Fetcher backed by the given Client. The client
// must not be nil; all operations are driven by the caller's context.
func NewFetcher(client Client) *Fetcher {
	return &Fetcher{client: client}
}

// Fetch retrieves and decodes all pages for the chapter identified by ref.
//
// ref.Provider is parsed as the engine-host's numeric sourceID (a
// disk-origin provider, whose Provider is a plain name rather than a
// number, has no live source and Fetch returns an error immediately without
// making any client call).
//
// Error semantics mirror the retired suwayomi.Fetcher:
//   - A non-numeric ref.Provider fails before any client call.
//   - If Pages returns an error, Fetch returns the zero ChapterPages and the
//     wrapped error without downloading any pages.
//   - If the source reports ZERO pages, Fetch returns an error wrapping
//     ErrNoPages — never a "downloaded" empty CBZ.
//   - If Image returns an error on any page, Fetch returns the zero
//     ChapterPages and the wrapped error. No partial result is ever
//     returned alongside a nil error.
//   - If ctx is cancelled or expired (checked before entering the page loop
//     and before each Image call), Fetch returns ctx.Err() wrapped in a
//     descriptive message.
func (f *Fetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	if err := ctx.Err(); err != nil {
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: context: %w", err)
	}

	sourceID, err := strconv.ParseInt(ref.Provider, 10, 64)
	if err != nil {
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: provider %q is not a live source id: %w", ref.Provider, err)
	}

	pages, err := f.client.Pages(ctx, sourceID, ref.URL)
	if err != nil {
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: pages: %w", err)
	}
	if len(pages) == 0 {
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: chapter %q: %w", ref.URL, ErrNoPages)
	}

	// Resolve the caller's per-page progress sink once (nil-safe no-op when
	// the caller set none), driven after each successful page so the
	// dispatcher can broadcast live download progress.
	progress := fetcher.ProgressFrom(ctx)

	images := make([]fetcher.PageImage, 0, len(pages))
	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: context: %w", err)
		}

		data, contentType, err := f.client.Image(ctx, sourceID, page.URL, page.ImageURL)
		if err != nil {
			return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: image: %w", err)
		}
		images = append(images, fetcher.PageImage{Data: data, Ext: extFromContentType(contentType)})

		// Report progress AFTER a page lands: len(images) is the running
		// count (1..len(pages)). The error path above returns before
		// reaching here, so a failed fetch emits nothing.
		progress(len(images), len(pages))
	}

	return fetcher.ChapterPages{Pages: images, PageCount: len(images)}, nil
}

// extFromContentType maps a page's response Content-Type to a bare file
// extension (no leading dot), matching the fetcher.PageImage.Ext convention.
// An unrecognised or empty content type falls back to "jpg" — the
// overwhelming majority of manga pages are JPEG, so this is a safe default
// rather than a hard failure over a header some sources omit or vary.
func extFromContentType(contentType string) string {
	mime, _, _ := strings.Cut(contentType, ";")
	switch strings.TrimSpace(mime) {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return "jpg"
	}
}
