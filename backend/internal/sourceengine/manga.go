package sourceengine

import "context"

// mangaRequest is the wire body for POST /manga.
type mangaRequest struct {
	SourceID    int64       `json:"sourceId"`
	URL         string      `json:"url"`
	AddressMode AddressMode `json:"addressMode"`
	WebURL      string      `json:"webUrl,omitempty"`
}

// MangaDetails calls POST /manga to fetch full metadata for the manga at url
// on sourceID.
func (c *httpClient) MangaDetails(ctx context.Context, sourceID int64, url string) (MangaDetails, error) {
	return c.MangaDetailsRef(ctx, ProviderRef{SourceID: sourceID, URL: url})
}

// MangaDetailsRef calls POST /manga with the complete persisted provider
// address. The response's AddressMode is the engine's successful resolution.
func (c *httpClient) MangaDetailsRef(ctx context.Context, ref ProviderRef) (MangaDetails, error) {
	return post[MangaDetails](ctx, c, "/manga", mangaRequest{
		SourceID: ref.SourceID, URL: ref.URL, AddressMode: ref.AddressMode, WebURL: ref.WebURL,
	})
}
