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
	Reader   bool   `json:"reader,omitempty"`
	SourceID int64  `json:"sourceId"`
	PageURL  string `json:"pageUrl"`
	ImageURL string `json:"imageUrl,omitempty"`
}

// Image calls POST /image with legacy address classification: a blank pageURL
// fetches a cover from imageURL; a nonblank pageURL fetches a reader page.
// Use ReaderImage for every address pair returned by Pages. The response is
// raw image bytes with its Content-Type, rather than JSON.
func (c *httpClient) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	return c.image(ctx, sourceID, pageURL, imageURL, false)
}

// ReaderImage fetches a reader page using the exact source-owned address pair,
// including sources that leave pageURL empty.
func (c *httpClient) ReaderImage(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	return c.image(ctx, sourceID, pageURL, imageURL, true)
}

func (c *httpClient) image(ctx context.Context, sourceID int64, pageURL, imageURL string, reader bool) ([]byte, string, error) {
	body := imageRequest{SourceID: sourceID, PageURL: pageURL, ImageURL: imageURL, Reader: reader}
	return doRaw(ctx, c, http.MethodPost, "/image", body)
}
