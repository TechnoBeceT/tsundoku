package mal

import (
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// Wire response shapes for MAL's v2 REST API. Kept separate from client.go
// per the codebase's "one thing per file" convention (mirrors
// internal/metadata/mal/mapper.go's split).

// dateLayout is MAL's date format for start_date/finish_date on a
// my_list_status ("YYYY-MM-DD"), used both to parse incoming values and to
// format outgoing ones.
const dateLayout = "2006-01-02"

type mainPictureData struct {
	Large string `json:"large"`
}

type mangaSearchNode struct {
	ID          int             `json:"id"`
	Title       string          `json:"title"`
	MainPicture mainPictureData `json:"main_picture"`
	NumChapters int             `json:"num_chapters"`
	Status      string          `json:"status"`
}

type mangaSearchEntry struct {
	Node mangaSearchNode `json:"node"`
}

type mangaListResponse struct {
	Data []mangaSearchEntry `json:"data"`
}

// myListStatus is MAL's my_list_status sub-object — present on a manga
// detail response only when the authenticated account has this manga on
// their list, and the SAME shape MAL's PUT .../my_list_status endpoint
// echoes back on a successful upsert.
type myListStatus struct {
	Status          string `json:"status"`
	Score           int    `json:"score"`
	NumChaptersRead int    `json:"num_chapters_read"`
	StartDate       string `json:"start_date"`
	FinishDate      string `json:"finish_date"`
}

type mangaDetail struct {
	ID           int           `json:"id"`
	Title        string        `json:"title"`
	NumChapters  int           `json:"num_chapters"`
	MyListStatus *myListStatus `json:"my_list_status"`
}

// mangaPageURL builds MAL's canonical manga page URL — the REST API itself
// never returns one (unlike AniList's siteUrl), so TrackSearchResult.URL /
// TrackEntry provenance is constructed from the id.
func mangaPageURL(id int) string {
	return "https://myanimelist.net/manga/" + strconv.Itoa(id)
}

// toTrackSearchResult maps one MAL search hit to the shared
// tracker.TrackSearchResult shape.
func toTrackSearchResult(n mangaSearchNode) tracker.TrackSearchResult {
	return tracker.TrackSearchResult{
		RemoteID:      strconv.Itoa(n.ID),
		Title:         n.Title,
		URL:           mangaPageURL(n.ID),
		CoverURL:      n.MainPicture.Large,
		Status:        n.Status,
		TotalChapters: n.NumChapters,
	}
}

// toTrackEntry maps a manga detail's my_list_status (plus the surrounding
// detail's id/title/chapter total) into the shared tracker.TrackEntry
// shape. remoteID is passed explicitly (not read off detail) so this same
// mapper also serves the upsert response, which carries no manga id of its
// own — only the my_list_status fields.
func toTrackEntry(remoteID string, s *myListStatus) tracker.TrackEntry {
	return tracker.TrackEntry{
		RemoteID:   remoteID,
		Status:     s.Status,
		Score:      float64(s.Score),
		Progress:   float64(s.NumChaptersRead),
		StartDate:  parseMALDate(s.StartDate),
		FinishDate: parseMALDate(s.FinishDate),
	}
}

// parseMALDate parses MAL's "YYYY-MM-DD" date string, returning nil for an
// empty or unparseable value — MAL leaves start_date/finish_date "" until
// the owner sets one, and a malformed date must never crash a mapping, only
// degrade to "unknown".
func parseMALDate(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, raw)
	if err != nil {
		return nil
	}
	return &t
}

// formatMALDate formats t as MAL's "YYYY-MM-DD", or "" for a nil t — MAL's
// form-encoded update endpoint takes an empty string to mean "leave this
// date unset", never omits the field.
func formatMALDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(dateLayout)
}
