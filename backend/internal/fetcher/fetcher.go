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
}

// PageImage holds the raw bytes and file extension of a single decoded page.
// Ext is the extension without a leading dot (e.g. "jpg", "png", "webp").
type PageImage struct {
	// Data is the raw image bytes for this page.
	Data []byte

	// Ext is the file extension without a leading dot (e.g. "jpg", "png").
	Ext string
}

// ChapterPages is the result of a successful Fetch call. It contains the
// ordered slice of decoded page images and the total page count. PageCount
// always equals len(Pages) for a successful response.
type ChapterPages struct {
	// Pages is the ordered list of decoded page images.
	Pages []PageImage

	// PageCount is the number of pages returned; equals len(Pages).
	PageCount int
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
