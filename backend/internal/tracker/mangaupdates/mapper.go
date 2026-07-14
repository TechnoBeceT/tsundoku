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
// reads. Type/Year/Description are the Search-Enrichment additions
// (confirmed present on MangaUpdates' own search response's "record" object
// via Komikku's MURecord.kt / Suwayomi-Server's MURecord.kt — no extra
// request param needed, the fields already ride along). DELIBERATELY left
// thinner than the other three trackers: the record also carries
// `bayesian_rating`, a genuine community score, but neither reference
// client maps it into a search-result score (Komikku's own MURecord.
// toTrackSearch reads the field yet never assigns it to `score`) — this
// port follows that same precedent rather than inventing a mapping neither
// proven implementation trusts, so toTrackSearchResult leaves Score at its
// zero value (unknown) for MangaUpdates.
type searchRecord struct {
	SeriesID    int64            `json:"series_id"`
	Title       string           `json:"title"`
	URL         string           `json:"url"`
	Image       searchCoverImage `json:"image"`
	Status      string           `json:"status"`
	Type        string           `json:"type"`
	Year        string           `json:"year"`
	Description string           `json:"description"`
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

// muSeriesRef identifies a series within a list-series request/response.
// The JSON key is "id" — NOT "series_id" (that key belongs to the
// UNRELATED /v1/series/search "record" object, searchRecord below).
// Confirmed against two independent, proven client ports of this same
// lists API (Komikku's + Suwayomi-Server's own MUSeries.kt, both
// `val id: Long? = null`) — this port previously tagged it "series_id",
// which silently zeroed every RemoteID read back off GetEntry/SaveEntry/
// UpdateEntry (the write still round-tripped via the caller's own input
// fallback in finishUpsert, masking the bug there).
type muSeriesRef struct {
	ID    int64  `json:"id"`
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
// GET /lists/series/{id} and echoed back by the add/update endpoints.
type listSeriesEntry struct {
	ListID int         `json:"list_id"`
	Series muSeriesRef `json:"series"`
	Status muStatus    `json:"status"`
}

// listSeriesAdd is the request-side shape POST /lists/series expects — an
// array of these. Adding a series to a list carries NO status object (only
// the target list); confirmed against the reference ports, whose
// addSeriesToList never sends chapter progress on bind.
type listSeriesAdd struct {
	Series muSeriesRef `json:"series"`
	ListID int         `json:"list_id"`
}

// listSeriesWrite is the request-side shape POST /lists/series/update
// expects — an array of these. UNLIKE listSeriesAdd, an update names BOTH
// the target list (list_id) AND the new progress (status.chapter) —
// MangaUpdates has no separate "just update progress" call.
type listSeriesWrite struct {
	Series muSeriesRef `json:"series"`
	ListID int         `json:"list_id"`
	Status muStatus    `json:"status"`
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
		RemoteID:    strconv.FormatInt(r.SeriesID, 10),
		Title:       r.Title,
		URL:         r.URL,
		CoverURL:    r.Image.URL.Original,
		Status:      r.Status,
		Type:        r.Type,
		StartDate:   r.Year,
		Description: r.Description,
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
