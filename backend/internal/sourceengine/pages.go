package sourceengine

import "context"

// pagesRequest is the wire body for POST /pages.
type pagesRequest struct {
	SourceID   int64  `json:"sourceId"`
	ChapterURL string `json:"chapterUrl"`
	// MangaURL is the series' provider-side URL. When non-empty the engine runs a
	// series-scoped fetch to repopulate per-chapter state (memo) some extensions
	// require in getPageList (GAP-109); it defaults to "" on the wire, preserving
	// the bare url-only behaviour when unknown.
	MangaURL string `json:"mangaUrl"`
	// AddressMode + WebURL describe MangaURL, not ChapterURL. They let the
	// engine warm a retained-state manga without guessing its address shape.
	AddressMode AddressMode `json:"addressMode"`
	WebURL      string      `json:"webUrl,omitempty"`
}

// pagesResponse is the wire envelope POST /pages wraps its result in
// ({"pages": [...]}). Pages unwraps it to a plain []Page.
type pagesResponse struct {
	Pages       []Page      `json:"pages"`
	AddressMode AddressMode `json:"addressMode"`
}

// PagesResult carries the page list and the address mode the engine successfully
// resolved when a retained-state manga warm-up was required.
type PagesResult struct {
	Pages       []Page
	AddressMode AddressMode
}

// Pages calls POST /pages to fetch the page list for the chapter at
// chapterURL on sourceID. Each returned Page's own (URL, ImageURL) pair must
// be fed back to Image verbatim — this call does not resolve image URLs.
//
// mangaURL is the series' provider-side URL; when non-empty the engine runs a
// series-scoped fetch to repopulate the per-chapter state (memo) some extensions
// require in getPageList (GAP-109). Pass "" when unknown — the engine then falls
// back to the bare url-only chapter seed exactly as before.
func (c *httpClient) Pages(ctx context.Context, sourceID int64, chapterURL, mangaURL string) ([]Page, error) {
	res, err := c.PagesRef(ctx, ProviderRef{SourceID: sourceID, URL: mangaURL}, chapterURL)
	if err != nil {
		return nil, err
	}
	return res.Pages, nil
}

// PagesRef calls POST /pages with the owning manga's complete persisted address
// and returns the engine-resolved mode alongside the page list.
func (c *httpClient) PagesRef(ctx context.Context, ref ProviderRef, chapterURL string) (PagesResult, error) {
	res, err := post[pagesResponse](ctx, c, "/pages", pagesRequest{
		SourceID: ref.SourceID, ChapterURL: chapterURL, MangaURL: ref.URL, AddressMode: ref.AddressMode, WebURL: ref.WebURL,
	})
	if err != nil {
		return PagesResult{}, err
	}
	return PagesResult{Pages: res.Pages, AddressMode: res.AddressMode}, nil
}
