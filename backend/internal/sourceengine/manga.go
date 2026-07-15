package sourceengine

import "context"

// mangaRequest is the wire body for POST /manga.
type mangaRequest struct {
	SourceID int64  `json:"sourceId"`
	URL      string `json:"url"`
}

// MangaDetails calls POST /manga to fetch full metadata for the manga at url
// on sourceID.
func (c *httpClient) MangaDetails(ctx context.Context, sourceID int64, url string) (MangaDetails, error) {
	return post[MangaDetails](ctx, c, "/manga", mangaRequest{SourceID: sourceID, URL: url})
}
