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
	// SupportsPrivate reports whether this tracker's entries can be marked
	// private on the remote account (AniList, Kitsu — true; MAL,
	// MangaUpdates — false, see tracker.Tracker.SupportsPrivate's own doc
	// comment). Surfaces the capability so the frontend can hide/disable a
	// "private" toggle for a tracker that would silently ignore it.
	SupportsPrivate bool `json:"supportsPrivate"`
}

// toTrackerDTO maps one registered Tracker (+ its TrackerConnection row, nil
// when the owner has never logged in) into its wire DTO. A disabled/
// unconfigured OAuth tracker (blank client-id) is NOT omitted — it is
// listed with isLoggedIn=false, so the owner still sees it in the list
// (spec/trackers-oauth-phase3 §4 — mirrors internal/tracker/providers'
// "always build all four trackers" doc comment).
func toTrackerDTO(t tracker.Tracker, conn *ent.TrackerConnection) TrackerDTO {
	dto := TrackerDTO{
		ID:              t.ID(),
		Name:            t.Name(),
		NeedsOAuth:      t.NeedsOAuth(),
		SupportsPrivate: t.SupportsPrivate(),
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
	ID              string  `json:"id"`
	SeriesID        string  `json:"seriesId"`
	TrackerID       int     `json:"trackerId"`
	TrackerName     string  `json:"trackerName"`
	RemoteID        string  `json:"remoteId"`
	RemoteURL       string  `json:"remoteUrl"`
	LibraryID       string  `json:"libraryId"`
	Title           string  `json:"title"`
	Status          string  `json:"status"`
	LastChapterRead float64 `json:"lastChapterRead"`
	TotalChapters   int     `json:"totalChapters"`
	Score           float64 `json:"score"`
	// ScoreFormat is the native scale Score is stored/must be written on for
	// this binding's tracker (one of the internal/tracker/sync.ScoreFormat
	// wire strings — e.g. "POINT_100", "KITSU_RATING_TWENTY", "MAL"),
	// resolved by resolveScoreFormat/resolveScoreFormats. The frontend's
	// score editor MUST read/write on this scale rather than assuming a
	// fixed 0-10 (the score-scale bug this field fixes: AniList is 0-100,
	// Kitsu is 0-20). "" only for a binding whose tracker is unregistered.
	ScoreFormat string     `json:"scoreFormat"`
	StartDate   *time.Time `json:"startDate"`
	FinishDate  *time.Time `json:"finishDate"`
	Private     bool       `json:"private"`
}

// toTrackBindingDTO maps one *ent.TrackBinding into its wire DTO, resolving
// TrackerName from the registry (falls back to "" for a binding whose
// tracker has since been unregistered — display-only, never an error: the
// bind service itself still surfaces ErrTrackerNotFound on any operation
// that needs a live Tracker). scoreFormat is the binding's ALREADY-resolved
// score format (see resolveScoreFormat/resolveScoreFormats in
// scoreformat.go) — this mapper never queries the DB itself, so it stays
// safe to call from a loop without becoming an N+1.
func toTrackBindingDTO(b *ent.TrackBinding, registry *tracker.Registry, scoreFormat string) TrackBindingDTO {
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
		ScoreFormat:     scoreFormat,
		StartDate:       b.StartDate,
		FinishDate:      b.FinishDate,
		Private:         b.Private,
	}
}

// toTrackBindingDTOs maps a whole series' binding set. Always returns a
// non-nil slice so the JSON renders [] rather than null when a series has
// no bindings. scoreFormats is trackerId→resolved-score-format (see
// resolveScoreFormats) — built ONCE by the caller from a single batch query,
// so mapping N bindings here never issues N queries.
func toTrackBindingDTOs(bindings []*ent.TrackBinding, registry *tracker.Registry, scoreFormats map[int]string) []TrackBindingDTO {
	out := make([]TrackBindingDTO, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, toTrackBindingDTO(b, registry, scoreFormats[b.TrackerID]))
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
	// Type is the tracker's own publication-format label ("" when absent —
	// see tracker.TrackSearchResult.Type's own doc comment).
	Type string `json:"type"`
	// StartDate is the tracker's reported publication-start year/date, kept
	// as a plain string so every tracker's native granularity survives
	// (see tracker.TrackSearchResult.StartDate's own doc comment).
	StartDate string `json:"startDate"`
	// Score is the catalog/community average rating, on the tracker's OWN
	// native scale (never normalized here — see
	// tracker.TrackSearchResult.Score's own doc comment). 0 = unknown.
	Score float64 `json:"score"`
	// Description is the tracker's own synopsis/summary text, verbatim.
	Description string `json:"description"`
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
		Type:          r.Type,
		StartDate:     r.StartDate,
		Score:         r.Score,
		Description:   r.Description,
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
