package sourceengine

import "context"

// chaptersRequest is the wire body for POST /chapters. MangaTitle is
// optional/additive (engine-host defaults it to "" when omitted) — it feeds
// the engine host's ChapterRecognition number-parsing step.
type chaptersRequest struct {
	SourceID   int64  `json:"sourceId"`
	URL        string `json:"url"`
	MangaTitle string `json:"mangaTitle,omitempty"`
}

// chaptersResponse is the wire envelope POST /chapters wraps its result in
// ({"chapters": [...]}). Chapters unwraps it to a plain []Chapter.
type chaptersResponse struct {
	Chapters []Chapter `json:"chapters"`
}

// Chapters calls POST /chapters to fetch the chapter list for the manga at
// url on sourceID. mangaTitle is passed through so the engine host's
// chapter-number recognition can strip it from a chapter name before
// number-matching; "" is safe (recognition still runs, just without the
// title-strip step).
func (c *httpClient) Chapters(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]Chapter, error) {
	res, err := post[chaptersResponse](ctx, c, "/chapters", chaptersRequest{SourceID: sourceID, URL: url, MangaTitle: mangaTitle})
	if err != nil {
		return nil, err
	}
	return res.Chapters, nil
}
