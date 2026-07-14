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
	// MediaType is MAL's publication-format label (e.g. "manga",
	// "one_shot", "manhwa") — Search-Enrichment addition.
	MediaType string `json:"media_type"`
	// StartDate is MAL's "YYYY-MM-DD" (sometimes "YYYY" or "YYYY-MM" —
	// MAL allows partial precision) publication-start date, kept RAW (not
	// parsed to time.Time — see tracker.TrackSearchResult.StartDate's own
	// doc comment on why every tracker's native granularity is preserved).
	StartDate string `json:"start_date"`
	// Mean is MAL's 0-10 community average score; 0 when MAL has no rating
	// data yet for this manga (MAL's own API omits the field entirely in
	// that case, which decodes to the zero value here).
	Mean float64 `json:"mean"`
	// Synopsis is MAL's plain-text summary.
	Synopsis string `json:"synopsis"`
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
		Type:          n.MediaType,
		StartDate:     n.StartDate,
		Score:         n.Mean,
		Description:   n.Synopsis,
	}
}

// toTrackEntry maps a manga detail's my_list_status (plus the surrounding
// detail's id/chapter total) into the shared tracker.TrackEntry shape.
// remoteID and totalChapters are passed explicitly (not read off a shared
// struct) so this same mapper also serves the upsert response, which
// carries no manga id or chapter total of its own — only the
// my_list_status fields; callers that lack a real total (upsertEntry, whose
// PUT response is my_list_status-only) pass through the value already on
// hand (the caller's own entry.TotalChapters) rather than silently
// dropping it. TotalChapters is what feeds Phase-4's auto-COMPLETED rule
// (total>0 && last==total, spec/trackers-and-rich-library-umbrella-v2 §6) —
// leaving it unset here was a Phase-3a gap (fixed in 3b): GetEntry's own
// manga-detail response always carries num_chapters, so there was no
// excuse for not threading it through.
func toTrackEntry(remoteID string, s *myListStatus, totalChapters int) tracker.TrackEntry {
	return tracker.TrackEntry{
		RemoteID:      remoteID,
		Status:        s.Status,
		Score:         float64(s.Score),
		Progress:      float64(s.NumChaptersRead),
		TotalChapters: totalChapters,
		StartDate:     parseMALDate(s.StartDate),
		FinishDate:    parseMALDate(s.FinishDate),
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
