package sourceengine

import "context"

// searchRequest is the wire body for POST /search.
type searchRequest struct {
	SourceID int64  `json:"sourceId"`
	Query    string `json:"query"`
	Page     int    `json:"page"`
}

// browseRequest is the wire body for POST /popular and POST /latest — a
// query-less catalogue listing request.
type browseRequest struct {
	SourceID int64 `json:"sourceId"`
	Page     int   `json:"page"`
}

// Search calls POST /search to search sourceID for query, returning one
// page of results. page is 1-based.
func (c *httpClient) Search(ctx context.Context, sourceID int64, query string, page int) (SearchResult, error) {
	return post[SearchResult](ctx, c, "/search", searchRequest{SourceID: sourceID, Query: query, Page: page})
}

// Popular calls POST /popular to fetch one page of sourceID's most-popular
// catalogue listing (no query). page is 1-based.
func (c *httpClient) Popular(ctx context.Context, sourceID int64, page int) (SearchResult, error) {
	return post[SearchResult](ctx, c, "/popular", browseRequest{SourceID: sourceID, Page: page})
}

// Latest calls POST /latest to fetch one page of sourceID's latest-updates
// catalogue listing (no query). page is 1-based.
func (c *httpClient) Latest(ctx context.Context, sourceID int64, page int) (SearchResult, error) {
	return post[SearchResult](ctx, c, "/latest", browseRequest{SourceID: sourceID, Page: page})
}
