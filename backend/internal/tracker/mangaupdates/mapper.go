package mangaupdates

import (
	"strconv"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// Wire request/response shapes for MangaUpdates' tracker-SYNC REST surface.
// Kept separate from client.go per the codebase's "one thing per file"
// convention (mirrors internal/tracker/mal/mapper.go's own split). These
// are DELIBERATELY REDEFINED here rather than shared with
// internal/metadata/mangaupdates' own wire types — the two packages read
// different endpoints (series detail/search vs. the lists API) and evolving
// one must never risk silently breaking the other (the same
// separate-package rationale the tracker/anilist and tracker/mal clients
// document for their own metadata siblings).

// loginRequest is the JSON body PUT /v1/account/login expects.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginContext is the "context" object a successful login response carries.
type loginContext struct {
	SessionToken string `json:"session_token"`
	UID          int64  `json:"uid"`
}

// loginResponse is the top-level envelope PUT /v1/account/login returns.
type loginResponse struct {
	Status  string       `json:"status"`
	Context loginContext `json:"context"`
}

// searchRequest is the JSON body POST /v1/series/search expects (mirrors
// internal/metadata/mangaupdates' own searchRequest).
type searchRequest struct {
	Search  string `json:"search"`
	PerPage int    `json:"perpage"`
}

// searchCoverImage mirrors MangaUpdates' "image" object shape.
type searchCoverImage struct {
	URL searchCoverImageURL `json:"url"`
}

// searchCoverImageURL holds the two size variants MangaUpdates serves; only
// Original is read.
type searchCoverImageURL struct {
	Original string `json:"original"`
}

// searchRecord is the subset of a search hit's "record" object this package
// reads.
type searchRecord struct {
	SeriesID int64            `json:"series_id"`
	Title    string           `json:"title"`
	URL      string           `json:"url"`
	Image    searchCoverImage `json:"image"`
	Status   string           `json:"status"`
}

// searchResultEntry is one hit in a search response — MangaUpdates nests
// the actual series fields under "record".
type searchResultEntry struct {
	Record searchRecord `json:"record"`
}

// searchResponse is the top-level envelope POST /v1/series/search returns.
type searchResponse struct {
	Results []searchResultEntry `json:"results"`
}

// muSeriesRef identifies a series within a list-series request/response —
// on write only the id is needed; on read MangaUpdates also echoes title/
// url.
type muSeriesRef struct {
	ID    int64  `json:"series_id"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

// muStatus is a list-series entry's reading-progress status object.
// MangaUpdates tracks volume alongside chapter; this port only reads/writes
// Chapter (TrackEntry.Progress has no separate volume dimension).
type muStatus struct {
	Volume  int `json:"volume,omitempty"`
	Chapter int `json:"chapter"`
}

// listSeriesEntry is one MangaUpdates list-series resource, as returned by
// GET /lists/{id}/series/{series_id} and echoed back by the add/update
// endpoints.
type listSeriesEntry struct {
	ListID int         `json:"list_id"`
	Series muSeriesRef `json:"series"`
	Status muStatus    `json:"status"`
}

// listSeriesWrite is the request-side shape POST /lists/{id}/series and
// POST /lists/{id}/series/update expect — an array of these.
type listSeriesWrite struct {
	Series muSeriesRef `json:"series"`
	Status muStatus    `json:"status"`
}

// listSeriesDelete is the request-side shape
// POST /lists/{id}/series/delete expects — an array of these (only the
// series identity is needed to remove an entry).
type listSeriesDelete struct {
	Series muSeriesRef `json:"series"`
}

// listStatusLabels names defaultListID's own list — currently the only one
// this client ever targets (see the package doc comment), so this is a
// single-entry lookup rather than the full {0:...,1:...,...} map; widen it
// if a future slice lets the owner choose a different list.
var listStatusLabels = map[int]string{
	0: "reading",
	1: "wish",
	2: "complete",
	3: "unfinished",
	4: "onhold",
}

// listStatusLabel returns the native-vocabulary label for a MangaUpdates
// list id, or "" for an unrecognized one — TrackEntry.Status stores this
// native label verbatim (spec: "store native scale/codes; convert only at
// display").
func listStatusLabel(listID int) string {
	return listStatusLabels[listID]
}

// toTrackSearchResult maps one MangaUpdates search hit's record to the
// shared tracker.TrackSearchResult shape. MangaUpdates' search response
// carries no chapter-count field, so TotalChapters is always 0 (unknown) —
// GetEntry after a bind is where a real total, if any, would come from a
// separate series-detail call this port does not make.
func toTrackSearchResult(r searchRecord) tracker.TrackSearchResult {
	return tracker.TrackSearchResult{
		RemoteID: strconv.FormatInt(r.SeriesID, 10),
		Title:    r.Title,
		URL:      r.URL,
		CoverURL: r.Image.URL.Original,
		Status:   r.Status,
	}
}

// toTrackEntry maps one MangaUpdates list-series entry to the shared
// tracker.TrackEntry shape. RemoteID/Title come from the entry's own series
// object (always echoed by MangaUpdates' list endpoints), never a
// caller-supplied fallback. LibraryID is always "" — MangaUpdates has no
// separate list-entry id the way AniList does; every write is keyed by the
// series id alone (mirrors mal.toTrackEntry's same LibraryID-less shape).
// Score is always 0: MangaUpdates' rating is a SEPARATE endpoint
// (POST /series/{id}/rating) this client does not call, so a list-series
// entry alone carries no score.
func toTrackEntry(e listSeriesEntry) tracker.TrackEntry {
	return tracker.TrackEntry{
		RemoteID: strconv.FormatInt(e.Series.ID, 10),
		Title:    e.Series.Title,
		Status:   listStatusLabel(e.ListID),
		Progress: float64(e.Status.Chapter),
	}
}
