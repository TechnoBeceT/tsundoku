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

// ErrNotLiveSource is returned when ref.Provider is not a numeric engine-host
// source id — i.e. a disk-origin provider (suwayomi_id == 0, whose Provider is a
// display NAME rather than a number). Such a provider has no live source to fetch
// from, so Fetch fails immediately before any client call. It is a distinct
// sentinel so the download dispatcher classifies it as a CHAPTER-SPECIFIC
// (budget-charging) failure rather than a transient source-down one: a wanted
// chapter whose only candidate is disk-origin genuinely cannot be downloaded and
// must eventually exhaust to permanently_failed, not retry forever on a cooldown.
var ErrNotLiveSource = errors.New("sourceengine: provider is not a live source id")

// Fetcher implements fetcher.ChapterFetcher by delegating to a Client. It
// resolves a chapter's ordered page list (reusing the caller's stored links when
// present, so a retry never re-hits the source's page-resolution step), then
// downloads each page's bytes to an on-disk per-chapter STAGING directory. A
// retry re-uses the pages already staged and re-fetches only the missing ones
// (true partial resume); the caller assembles the CBZ from the returned pages
// and then deletes the staging dir, so the byte cache holds bytes ONLY for
// in-progress chapters (bounded, disk-backed, self-cleaning).
type Fetcher struct {
	client Client
	// stagingRoot is the directory each chapter's staging dir lives under
	// (<stagingRoot>/<providerChapterID>/). In production it is a hidden dir under
	// the library storage root, so it shares the library's filesystem (the atomic
	// temp→rename works) and is skipped by the library scanner/reconcile.
	stagingRoot string
}

// NewFetcher constructs a Fetcher backed by the given Client, staging downloaded
// page bytes under stagingRoot. The client must not be nil; all operations are
// driven by the caller's context.
func NewFetcher(client Client, stagingRoot string) *Fetcher {
	return &Fetcher{client: client, stagingRoot: stagingRoot}
}

// Fetch retrieves and decodes all pages for the chapter identified by ref.
//
// ref.Provider is parsed as the engine-host's numeric sourceID (a
// disk-origin provider, whose Provider is a plain name rather than a
// number, has no live source and Fetch returns an error immediately without
// making any client call).
//
// Link re-use: when ref.PageLinks is non-empty (a prior attempt already resolved
// them) Fetch SKIPS the Client.Pages call entirely and drives the image loop from
// those links — the Cloudflare-protected page-resolution step is paid at most
// once per chapter per source. When empty, Fetch calls Pages once and returns the
// resolved list in ChapterPages.PageLinks for the caller to persist.
//
// Byte staging + resume: each downloaded page is streamed to an on-disk staging
// file; a page already staged from a prior attempt is re-used without re-fetching.
// The returned ChapterPages.StagingDir names that directory so the caller deletes
// it once the CBZ is assembled (and KEEPS it on failure, so the next attempt
// resumes). PageLinks + StagingDir are populated even when Fetch returns an error
// (after the list was resolved), so the caller can still persist the links and
// resume; only Pages is all-or-nothing (empty on any error).
//
// Error semantics mirror the retired suwayomi.Fetcher:
//   - A non-numeric ref.Provider fails before any client call.
//   - If Pages returns an error, Fetch returns the zero ChapterPages and the
//     wrapped error without downloading any pages.
//   - If the source reports ZERO pages, Fetch returns an error wrapping
//     ErrNoPages — never a "downloaded" empty CBZ.
//   - If Image returns a TRANSIENT error on a page (timeout / network /
//     server_error), that ONE page is retried up to imageRetries times with a flat
//     imageRetryBackoff (respecting ctx) before the chapter fails; a survivor is
//     wrapped in ErrImageFetch so the dispatcher treats it as CHAPTER-SPECIFIC (it
//     charges the budget but never trips the source breaker — see ErrImageFetch). A
//     ban-class image error (captcha / rate_limit) or a chapter-specific one
//     (not_found / parse) is NOT retried and falls straight through. On any surviving
//     Image error Fetch returns an empty Pages slice — no partial page set is ever
//     returned.
//   - If any page fails image validation (empty, truncated, non-image, or a
//     decompression-bomb-sized body — see imagevalidate.go), Fetch returns an
//     error wrapping ErrBrokenPage, so the whole chapter attempt fails cleanly
//     rather than persisting a broken panel. The existing per-source retry +
//     cross-source fall-through then drives the chapter to a COMPLETE download.
//   - If ctx is cancelled or expired (checked before entering the page loop
//     and before each Image call), Fetch returns ctx.Err() wrapped in a
//     descriptive message.
func (f *Fetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	if err := ctx.Err(); err != nil {
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: context: %w", err)
	}

	sourceID, err := strconv.ParseInt(ref.Provider, 10, 64)
	if err != nil {
		// Wrap the SENTINEL (not the raw strconv error) so the dispatcher can
		// errors.Is it and charge the retry budget — see ErrNotLiveSource.
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: provider %q is not a live source id: %w", ref.Provider, ErrNotLiveSource)
	}

	links, err := f.resolveLinks(ctx, sourceID, ref)
	if err != nil {
		return fetcher.ChapterPages{}, err
	}
	if len(links) == 0 {
		return fetcher.ChapterPages{}, fmt.Errorf("sourceengine fetcher: chapter %q: %w", ref.URL, ErrNoPages)
	}

	// From here on the page list is resolved, so carry links + stagingDir on every
	// return path (success OR failure): the caller persists the links (skip
	// re-resolution next attempt) and keeps/cleans the staging dir accordingly.
	stagingDir := f.stagingDirFor(ref)
	result := fetcher.ChapterPages{PageLinks: links, StagingDir: stagingDir}

	// Download every missing page to the staging dir (re-using pages a prior
	// attempt already staged), holding at most one page in memory at a time.
	if err := f.stagePages(ctx, sourceID, links, stagingDir); err != nil {
		return result, err
	}

	// Collect the staged pages into memory in link order for the caller's CBZ
	// render (disk.RenderChapter needs the full ordered slice).
	images, err := collectStagedPages(links, stagingDir)
	if err != nil {
		return result, err
	}

	result.Pages = images
	result.PageCount = len(images)
	return result, nil
}

// resolveLinks returns the chapter's ordered page list. It re-uses ref.PageLinks
// when present (SKIPPING the Client.Pages call — a retry never re-hits the
// source's page-resolution step) and otherwise calls Pages once, mapping the
// result to the persistable fetcher.PageLink shape.
func (f *Fetcher) resolveLinks(ctx context.Context, sourceID int64, ref fetcher.FetchRef) ([]fetcher.PageLink, error) {
	if len(ref.PageLinks) > 0 {
		return ref.PageLinks, nil
	}
	pages, err := f.client.Pages(ctx, sourceID, ref.URL)
	if err != nil {
		return nil, fmt.Errorf("sourceengine fetcher: pages: %w", err)
	}
	links := make([]fetcher.PageLink, len(pages))
	for i, p := range pages {
		links[i] = fetcher.PageLink{URL: p.URL, ImageURL: p.ImageURL}
	}
	return links, nil
}

// stagePages downloads every page NOT already present in the staging dir, writing
// each atomically to disk (temp→fsync→rename), and re-uses staged pages from a
// prior attempt without re-fetching them. It reports progress after each page is
// present so the caller's live progress reflects resumed pages too. It holds at
// most one page's bytes in memory at a time. Any Image or validation failure
// returns immediately (all-or-nothing) — the staged pages so far are KEPT for the
// next attempt's resume.
func (f *Fetcher) stagePages(ctx context.Context, sourceID int64, links []fetcher.PageLink, stagingDir string) error {
	staged, err := scanStagingDir(stagingDir)
	if err != nil {
		return err
	}
	progress := fetcher.ProgressFrom(ctx)
	total := len(links)
	for i, link := range links {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sourceengine fetcher: context: %w", err)
		}

		if _, ok := staged[i]; ok {
			// Already downloaded on a prior attempt — re-use it, no source call.
			progress(i+1, total)
			continue
		}

		data, contentType, err := f.fetchImageRetrying(ctx, sourceID, link)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The parent context was cancelled/expired mid-fetch (graceful
				// shutdown) — NOT the page's fault. Return the context error
				// unwrapped so the dispatcher neither charges this chapter's budget
				// nor trips the breaker (both gate on ctx.Err()==nil / classification).
				return fmt.Errorf("sourceengine fetcher: context: %w", ctxErr)
			}
			if isTransientImageError(err) {
				// A transient byte-fetch failure that survived the retries. Reaching
				// this stage proves the source session is alive (Pages already
				// succeeded), so mark it CHAPTER-SPECIFIC via ErrImageFetch: the
				// dispatcher charges the per-source budget but does NOT trip the
				// breaker for one flaky page. See ErrImageFetch.
				return fmt.Errorf("sourceengine fetcher: image: %w: %v", ErrImageFetch, err)
			}
			// A ban-class image failure (captcha / rate_limit) stays SOURCE-WIDE so it
			// still trips the breaker; a chapter-specific one (not_found / parse) is
			// already classified chapter-specific by errorclass. Neither is wrapped.
			return fmt.Errorf("sourceengine fetcher: image: %w", err)
		}
		// Prove the page is a complete, decodable image BEFORE it is staged, so a
		// truncated body / HTML challenge served as 200 / 0-byte body never enters a
		// CBZ. See imagevalidate.go.
		if err := validateImagePage(data); err != nil {
			return fmt.Errorf("sourceengine fetcher: page %d: %w", i, err)
		}
		if err := stageWrite(stagingDir, i, extFromContentType(contentType), data); err != nil {
			return err
		}
		progress(i+1, total)
	}
	return nil
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
