package sourceengine

import "context"

// chaptersRequest is the wire body for POST /chapters.
type chaptersRequest struct {
	SourceID int64  `json:"sourceId"`
	URL      string `json:"url"`
}

// chaptersResponse is the wire envelope POST /chapters wraps its result in
// ({"chapters": [...]}). Chapters unwraps it to a plain []Chapter.
type chaptersResponse struct {
	Chapters []Chapter `json:"chapters"`
}

// Chapters calls POST /chapters to fetch the chapter list for the manga at
// url on sourceID.
func (c *httpClient) Chapters(ctx context.Context, sourceID int64, url string) ([]Chapter, error) {
	res, err := post[chaptersResponse](ctx, c, "/chapters", chaptersRequest{SourceID: sourceID, URL: url})
	if err != nil {
		return nil, err
	}
	return res.Chapters, nil
}
