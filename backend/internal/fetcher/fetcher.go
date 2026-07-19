// Package fetcher defines the ChapterFetcher port — the clean boundary between
// the M1 download dispatcher and the page-fetch engine (Suwayomi in M2).
//
// Only pure types and the interface live here. No network, HTTP, or
// Suwayomi-specific code may be imported by this package. M2 will supply a
// concrete implementation; M1 tests use the deterministic fake in the fake/
// sub-package.
package fetcher

import (
	"context"

	"github.com/google/uuid"
)

// PageLink is one resolved page's source address pair — the (URL, ImageURL)
// the engine's image-download call needs. It is the persisted, retry-stable form
// of a resolved page list: storing it on ProviderChapter.page_links lets a retry
// SKIP the (Cloudflare-protected) page-resolution call entirely and go straight to
// the image loop. The JSON tags match sourceengine.Page's so the two round-trip
// losslessly.
type PageLink struct {
	// URL is the source's own page address.
	URL string `json:"url"`

	// ImageURL is the resolved image address; "" when the source only sets URL
	// and resolves the real image server-side.
	ImageURL string `json:"imageUrl"`
}

// FetchRef identifies a single ProviderChapter whose pages should be fetched.
// It carries all the information a fetcher implementation needs to locate and
// authenticate the request: the provider name, scanlator/language metadata,
// the provider-side URL, the Suwayomi chapter ID, and the internal
// SeriesProvider row that owns this chapter.
type FetchRef struct {
	// Provider is the name of the source provider (e.g. "mangadex").
	Provider string

	// Scanlator is the name of the scanlation group, if known.
	Scanlator string

	// Language is the BCP-47 language tag for this chapter (e.g. "en").
	Language string

	// URL is the provider-supplied canonical URL for this chapter.
	URL string

	// SuwayomiID is the Suwayomi-internal numeric identifier for this chapter.
	// It is used by the M2 Suwayomi fetcher implementation and is left zero in
	// contexts where Suwayomi has not been consulted.
	SuwayomiID int

	// SeriesProviderID is the UUID of the SeriesProvider row that owns this
	// ProviderChapter, used for database correlation.
	SeriesProviderID uuid.UUID

	// ProviderChapterID is the UUID of the ProviderChapter row being fetched. It
	// names this chapter's on-disk STAGING directory (one dir per provider-chapter
	// under the fetcher's staging root), so a retry resumes into the same dir and
	// re-uses the pages it already downloaded.
	ProviderChapterID uuid.UUID

	// PageLinks holds this chapter's already-resolved page list (from a prior
	// attempt's page-resolution call, persisted on ProviderChapter.page_links).
	// When non-empty a fetcher SKIPS re-resolving pages and drives the image loop
	// from these directly — the whole point of the byte-cache fix: a retry never
	// re-hits the source's (Cloudflare-protected) page-resolution step. Empty on a
	// first attempt, when the fetcher resolves the list itself and returns it in
	// ChapterPages.PageLinks for the caller to persist.
	PageLinks []PageLink
}

// PageImage holds the raw bytes and file extension of a single decoded page.
// Ext is the extension without a leading dot (e.g. "jpg", "png", "webp").
type PageImage struct {
	// Data is the raw image bytes for this page.
	Data []byte

	// Ext is the file extension without a leading dot (e.g. "jpg", "png").
	Ext string
}

// ChapterPages is the result of a Fetch call. On success it contains the ordered
// slice of decoded page images and the total page count. It ALSO carries the
// resolved PageLinks and StagingDir, which are set even when Fetch returns an
// error (after the page list was resolved), so the caller can persist the links
// (skip re-resolution on retry) and clean up / resume the staging dir.
type ChapterPages struct {
	// Pages is the ordered list of decoded page images. Empty on any error
	// (all-or-nothing: a partial image set is never returned).
	Pages []PageImage

	// PageCount is the number of pages returned; equals len(Pages) on success.
	PageCount int

	// PageLinks is the ordered resolved page list this Fetch used — the stored
	// links when they were reused, or the freshly-resolved links otherwise. The
	// caller persists these onto ProviderChapter.page_links when they were newly
	// resolved, so a retry skips the page-resolution call. Set even on error (once
	// the list was resolved), never nil on a chapter that has any pages.
	PageLinks []PageLink

	// StagingDir is the absolute path to this chapter's on-disk page-staging
	// directory. The caller deletes it after the CBZ is assembled (so the byte
	// cache holds bytes only for in-progress chapters); on failure it is KEPT so
	// the next attempt resumes from the pages already downloaded. "" when the
	// fetcher does not stage (e.g. the in-memory test fake).
	StagingDir string
}

// ChapterFetcher is the port that the M1 download dispatcher calls to retrieve
// the decoded pages for a chapter. M1 ships only the deterministic fake
// implementation (see the fake/ sub-package). M2 will provide a concrete
// Suwayomi-backed implementation without changing this interface.
type ChapterFetcher interface {
	// Fetch retrieves and decodes all pages for the chapter identified by ref.
	// It returns a ChapterPages containing the ordered page images, or an error
	// if the chapter could not be fetched. The caller is responsible for
	// cancelling the context to abort an in-flight fetch.
	Fetch(ctx context.Context, ref FetchRef) (ChapterPages, error)
}
