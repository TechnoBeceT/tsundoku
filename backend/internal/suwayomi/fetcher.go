// Package suwayomi — real ChapterFetcher implementation over the Suwayomi client.
//
// This file provides Fetcher, the concrete implementation of the
// fetcher.ChapterFetcher port defined by M1. It calls the Suwayomi client to
// retrieve page URLs and then downloads each page image, assembling the result
// into a fetcher.ChapterPages value.
//
// Errors from any step (ChapterPages or PageBytes) are propagated immediately;
// no partial result is ever returned with a nil error.
package suwayomi

import (
	"context"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// Compile-time assertion: Fetcher must satisfy the M1 fetcher.ChapterFetcher port.
var _ fetcher.ChapterFetcher = (*Fetcher)(nil)

// ErrNoPages is returned when a source reports a chapter with zero pages (G4). It
// is a fetch failure — a zero-page chapter must retry / fall through, never render
// a "downloaded" empty CBZ.
var ErrNoPages = errors.New("suwayomi: chapter has no pages")

// Fetcher implements fetcher.ChapterFetcher by delegating to a Suwayomi Client.
// It converts a fetcher.FetchRef into decoded page images by calling
// Client.ChapterPages to obtain the ordered page-URL list, then Client.PageBytes
// for each URL to download the raw image bytes.
type Fetcher struct {
	client Client
}

// NewFetcher constructs a Fetcher backed by the given Suwayomi client.
// The client must not be nil; all operations are driven by the client's context.
func NewFetcher(client Client) *Fetcher {
	return &Fetcher{client: client}
}

// Fetch retrieves and decodes all pages for the chapter identified by ref.
//
// It calls client.ChapterPages with ref.SuwayomiID to obtain the ordered list
// of page-image URLs, then calls client.PageBytes for each URL to download the
// raw bytes and detect the file extension.
//
// Error semantics:
//   - If ChapterPages returns an error, Fetch returns the zero ChapterPages and
//     the wrapped error without downloading any pages.
//   - If the source reports ZERO pages (G4), Fetch returns an error: a zero-page
//     chapter must fail the attempt (so retry + fall-through run), never render a
//     "downloaded" empty CBZ.
//   - If PageBytes returns an error on any page k, Fetch returns the zero
//     ChapterPages and the wrapped error. No partial result (the first k-1 pages)
//     is ever returned alongside a nil error.
//   - If any page fails image validation (G1–G3 — truncated, non-image, or empty
//     body), Fetch returns the zero ChapterPages and an error wrapping
//     ErrBrokenPage, so the whole chapter attempt fails cleanly rather than
//     persisting a broken panel. The existing per-source retry + cross-source
//     fall-through then drives the chapter to a COMPLETE download.
//   - If ctx is cancelled or expired (checked before entering the page loop and
//     before each PageBytes call), Fetch returns ctx.Err() wrapped in a
//     descriptive message.
func (f *Fetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	// Check ctx before any I/O so a pre-cancelled context aborts immediately.
	if err := ctx.Err(); err != nil {
		return fetcher.ChapterPages{}, fmt.Errorf("suwayomi fetcher: context: %w", err)
	}

	urls, err := f.client.ChapterPages(ctx, ref.SuwayomiID)
	if err != nil {
		return fetcher.ChapterPages{}, fmt.Errorf("suwayomi fetcher: chapter pages: %w", err)
	}

	// G4: a chapter whose source returns no pages must FAIL the attempt. Rendering
	// zero pages would write a "downloaded" CBZ containing only ComicInfo.xml (a
	// missing chapter). Failing here lets the caller retry / fall through to another
	// source instead.
	if len(urls) == 0 {
		return fetcher.ChapterPages{}, fmt.Errorf("suwayomi fetcher: chapter %d: %w", ref.SuwayomiID, ErrNoPages)
	}

	// Resolve the caller's per-page progress sink once (nil-safe no-op when the
	// caller set none). It is driven after each successful page below, so the
	// dispatcher can broadcast live download progress.
	progress := fetcher.ProgressFrom(ctx)

	pages := make([]fetcher.PageImage, 0, len(urls))
	for _, url := range urls {
		// Re-check ctx before each page download so cancellation is honoured
		// promptly on a multi-page chapter even when the client calls succeed
		// quickly (e.g. in tests with a stub that does not block on ctx).
		if err := ctx.Err(); err != nil {
			return fetcher.ChapterPages{}, fmt.Errorf("suwayomi fetcher: context: %w", err)
		}

		data, ext, err := f.client.PageBytes(ctx, url)
		if err != nil {
			return fetcher.ChapterPages{}, fmt.Errorf("suwayomi fetcher: page bytes: %w", err)
		}

		// G1–G3: prove the page is a complete, decodable image BEFORE it enters the
		// result. A truncated body, an HTML challenge page served as 200, or a 0-byte
		// body all pass PageBytes' transport checks but must never become a panel; on
		// any such page fail the WHOLE chapter (all-or-nothing) so retry + fall-through
		// deliver a complete chapter from this or another source.
		if err := validateImagePage(data); err != nil {
			return fetcher.ChapterPages{}, fmt.Errorf("suwayomi fetcher: page %d: %w", len(pages), err)
		}
		pages = append(pages, fetcher.PageImage{Data: data, Ext: ext})

		// Report progress AFTER a page lands: len(pages) is the running count
		// (1..len(urls)). Only successful pages advance the count — the error path
		// above returns before reaching here, so a failed fetch emits nothing.
		progress(len(pages), len(urls))
	}

	return fetcher.ChapterPages{
		Pages:     pages,
		PageCount: len(pages),
	}, nil
}
