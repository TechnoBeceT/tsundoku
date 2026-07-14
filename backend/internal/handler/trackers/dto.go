package trackers

import (
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// TrackerDTO is one registered tracker's connect status — the JSON shape
// returned by GET /api/trackers and (refreshed) by the login endpoints.
// Mirrors the Suwayomi settings/extensions DTOs' plain camelCase mapping.
type TrackerDTO struct {
	// ID is the tracker's stable numeric registry id (tracker.ID* constants).
	ID int `json:"id"`
	// Name is the tracker's human-display name (e.g. "AniList").
	Name string `json:"name"`
	// NeedsOAuth reports whether this tracker connects via an OAuth redirect
	// (AniList, MAL) as opposed to a direct username/password login (Kitsu,
	// MangaUpdates).
	NeedsOAuth bool `json:"needsOAuth"`
	// IsLoggedIn reports whether a TrackerConnection row exists for this
	// tracker — the owner has connected an account.
	IsLoggedIn bool `json:"isLoggedIn"`
	// IsTokenExpired mirrors TrackerConnection.token_expired: set when a
	// token refresh has failed and the owner must re-login. Always false
	// when IsLoggedIn is false.
	IsTokenExpired bool `json:"isTokenExpired"`
	// Username is the connected account's display username ("" when not
	// logged in).
	Username string `json:"username"`
}

// toTrackerDTO maps one registered Tracker (+ its TrackerConnection row, nil
// when the owner has never logged in) into its wire DTO. A disabled/
// unconfigured OAuth tracker (blank client-id) is NOT omitted — it is
// listed with isLoggedIn=false, so the owner still sees it in the list
// (spec/trackers-oauth-phase3 §4 — mirrors internal/tracker/providers'
// "always build all four trackers" doc comment).
func toTrackerDTO(t tracker.Tracker, conn *ent.TrackerConnection) TrackerDTO {
	dto := TrackerDTO{
		ID:         t.ID(),
		Name:       t.Name(),
		NeedsOAuth: t.NeedsOAuth(),
	}
	if conn != nil {
		dto.IsLoggedIn = true
		dto.IsTokenExpired = conn.TokenExpired
		dto.Username = conn.Username
	}
	return dto
}

// TrackerAuthURLDTO is the JSON shape returned by GET
// /api/trackers/:id/auth-url — the authorize URL to send the owner's
// browser to.
type TrackerAuthURLDTO struct {
	AuthURL string `json:"authUrl"`
}

// TrackBindingDTO is one series↔tracker binding — the JSON shape returned by
// GET/POST /api/series/:id/tracking and the refresh endpoint. Every
// status/score/progress field is the tracker's OWN native scale (spec §2 —
// converted only at display, never here).
type TrackBindingDTO struct {
	ID              string     `json:"id"`
	SeriesID        string     `json:"seriesId"`
	TrackerID       int        `json:"trackerId"`
	TrackerName     string     `json:"trackerName"`
	RemoteID        string     `json:"remoteId"`
	RemoteURL       string     `json:"remoteUrl"`
	LibraryID       string     `json:"libraryId"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	LastChapterRead float64    `json:"lastChapterRead"`
	TotalChapters   int        `json:"totalChapters"`
	Score           float64    `json:"score"`
	StartDate       *time.Time `json:"startDate"`
	FinishDate      *time.Time `json:"finishDate"`
	Private         bool       `json:"private"`
}

// toTrackBindingDTO maps one *ent.TrackBinding into its wire DTO, resolving
// TrackerName from the registry (falls back to "" for a binding whose
// tracker has since been unregistered — display-only, never an error: the
// bind service itself still surfaces ErrTrackerNotFound on any operation
// that needs a live Tracker).
func toTrackBindingDTO(b *ent.TrackBinding, registry *tracker.Registry) TrackBindingDTO {
	var trackerName string
	if t, ok := registry.ByID(b.TrackerID); ok {
		trackerName = t.Name()
	}
	return TrackBindingDTO{
		ID:              b.ID.String(),
		SeriesID:        b.SeriesID.String(),
		TrackerID:       b.TrackerID,
		TrackerName:     trackerName,
		RemoteID:        b.RemoteID,
		RemoteURL:       b.RemoteURL,
		LibraryID:       b.LibraryID,
		Title:           b.Title,
		Status:          b.Status,
		LastChapterRead: b.LastChapterRead,
		TotalChapters:   b.TotalChapters,
		Score:           b.Score,
		StartDate:       b.StartDate,
		FinishDate:      b.FinishDate,
		Private:         b.Private,
	}
}

// toTrackBindingDTOs maps a whole series' binding set. Always returns a
// non-nil slice so the JSON renders [] rather than null when a series has
// no bindings.
func toTrackBindingDTOs(bindings []*ent.TrackBinding, registry *tracker.Registry) []TrackBindingDTO {
	out := make([]TrackBindingDTO, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, toTrackBindingDTO(b, registry))
	}
	return out
}

// TrackSearchResultDTO is one tracker's search hit — the JSON shape returned
// by GET /api/trackers/:id/search. Mirrors tracker.TrackSearchResult with
// camelCase JSON tags.
type TrackSearchResultDTO struct {
	RemoteID string `json:"remoteId"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	CoverURL string `json:"coverUrl"`
	// Status is the tracker's own native status vocabulary — never
	// normalized here (spec §2).
	Status string `json:"status"`
	// TotalChapters is the tracker's reported total; 0 = unknown/ongoing.
	TotalChapters int `json:"totalChapters"`
}

// toTrackSearchResultDTO maps one tracker.TrackSearchResult into its wire DTO.
func toTrackSearchResultDTO(r tracker.TrackSearchResult) TrackSearchResultDTO {
	return TrackSearchResultDTO{
		RemoteID:      r.RemoteID,
		Title:         r.Title,
		URL:           r.URL,
		CoverURL:      r.CoverURL,
		Status:        r.Status,
		TotalChapters: r.TotalChapters,
	}
}

// toTrackSearchResultDTOs maps a whole search-hit list. Always returns a
// non-nil slice so the JSON renders [] rather than null.
func toTrackSearchResultDTOs(results []tracker.TrackSearchResult) []TrackSearchResultDTO {
	out := make([]TrackSearchResultDTO, 0, len(results))
	for _, r := range results {
		out = append(out, toTrackSearchResultDTO(r))
	}
	return out
}
