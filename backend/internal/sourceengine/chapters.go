package sourceengine

import "context"

// chaptersRequest is the wire body for POST /chapters. MangaTitle is
// optional/additive (engine-host defaults it to "" when omitted) — it feeds
// the engine host's ChapterRecognition number-parsing step.
type chaptersRequest struct {
	SourceID    int64       `json:"sourceId"`
	URL         string      `json:"url"`
	MangaTitle  string      `json:"mangaTitle,omitempty"`
	AddressMode AddressMode `json:"addressMode"`
	WebURL      string      `json:"webUrl,omitempty"`
}

// chaptersResponse is the wire envelope POST /chapters wraps its result in
// ({"chapters": [...]}). Chapters unwraps it to a plain []Chapter.
type chaptersResponse struct {
	Chapters    []Chapter   `json:"chapters"`
	AddressMode AddressMode `json:"addressMode"`
}

// ChaptersResult carries the chapter list and the address mode the engine
// successfully resolved for the provider manga.
type ChaptersResult struct {
	Chapters    []Chapter
	AddressMode AddressMode
}

// Chapters calls POST /chapters to fetch the chapter list for the manga at
// url on sourceID. mangaTitle is passed through so the engine host's
// chapter-number recognition can strip it from a chapter name before
// number-matching; "" is safe (recognition still runs, just without the
// title-strip step).
func (c *httpClient) Chapters(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]Chapter, error) {
	res, err := c.ChaptersRef(ctx, ProviderRef{SourceID: sourceID, URL: url}, mangaTitle)
	if err != nil {
		return nil, err
	}
	return res.Chapters, nil
}

// ChaptersRef calls POST /chapters with the complete persisted provider
// address and returns both chapters and the engine-resolved address mode.
func (c *httpClient) ChaptersRef(ctx context.Context, ref ProviderRef, mangaTitle string) (ChaptersResult, error) {
	res, err := post[chaptersResponse](ctx, c, "/chapters", chaptersRequest{
		SourceID: ref.SourceID, URL: ref.URL, MangaTitle: mangaTitle, AddressMode: ref.AddressMode, WebURL: ref.WebURL,
	})
	if err != nil {
		return ChaptersResult{}, err
	}
	return ChaptersResult{Chapters: res.Chapters, AddressMode: res.AddressMode}, nil
}
