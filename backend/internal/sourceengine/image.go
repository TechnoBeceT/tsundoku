package sourceengine

import (
	"context"
	"net/http"
)

// imageRequest is the wire body for POST /image. ImageURL uses omitempty so
// an empty imageURL (the caller has only a page.url) is OMITTED from the
// JSON body rather than sent as ""; the engine host treats an absent
// imageUrl as null and resolves the real image address itself.
type imageRequest struct {
	SourceID int64  `json:"sourceId"`
	PageURL  string `json:"pageUrl"`
	ImageURL string `json:"imageUrl,omitempty"`
}

// Image calls POST /image to download the raw bytes for one page, addressed
// by the same (pageURL, imageURL) pair a Pages call returned. Unlike every
// other endpoint, the response body IS the raw image bytes (not JSON); the
// Content-Type header is returned alongside so callers can tell what they
// downloaded.
func (c *httpClient) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	body := imageRequest{SourceID: sourceID, PageURL: pageURL, ImageURL: imageURL}
	return doRaw(ctx, c, http.MethodPost, "/image", body)
}
