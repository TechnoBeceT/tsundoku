package sourceengine

import "context"

// pagesRequest is the wire body for POST /pages.
type pagesRequest struct {
	SourceID   int64  `json:"sourceId"`
	ChapterURL string `json:"chapterUrl"`
}

// pagesResponse is the wire envelope POST /pages wraps its result in
// ({"pages": [...]}). Pages unwraps it to a plain []Page.
type pagesResponse struct {
	Pages []Page `json:"pages"`
}

// Pages calls POST /pages to fetch the page list for the chapter at
// chapterURL on sourceID. Each returned Page's own (URL, ImageURL) pair must
// be fed back to Image verbatim — this call does not resolve image URLs.
func (c *httpClient) Pages(ctx context.Context, sourceID int64, chapterURL string) ([]Page, error) {
	res, err := post[pagesResponse](ctx, c, "/pages", pagesRequest{SourceID: sourceID, ChapterURL: chapterURL})
	if err != nil {
		return nil, err
	}
	return res.Pages, nil
}
