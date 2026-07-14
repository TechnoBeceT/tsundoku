package kitsu

import (
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// Wire response/request shapes for Kitsu's tracker-SYNC JSON:API surface.
// Kept separate from client.go per the codebase's "one thing per file"
// convention (mirrors internal/tracker/mal/mapper.go's own split).

// tokenResponse is Kitsu's OAuth token endpoint's JSON response shape.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// mangaSearchAttrs is the subset of a manga resource's `attributes` bag
// Search reads.
type mangaSearchAttrs struct {
	Slug           string      `json:"slug"`
	CanonicalTitle string      `json:"canonicalTitle"`
	Status         string      `json:"status"`
	ChapterCount   *int        `json:"chapterCount"`
	PosterImage    posterImage `json:"posterImage"`
	// Subtype is Kitsu's publication-format label (e.g. "manga", "manhwa",
	// "manhua", "novel", "oneshot") — Search-Enrichment addition.
	Subtype string `json:"subtype"`
	// StartDate is Kitsu's "YYYY-MM-DD" publication-start date, kept RAW
	// (mirrors internal/metadata/kitsu's own mangaAttrs.StartDate shape).
	StartDate string `json:"startDate"`
	// AverageRating is a "0".."100" STRING percentage (e.g. "85.07"), NOT a
	// JSON number — the same Kitsu API quirk internal/metadata/kitsu's
	// mangaAttrs.AverageRating documents (confirmed live there via
	// TestShapeKitsu); this package re-declares its own copy rather than
	// importing that one, per this file's own "deliberately redefined"
	// convention for wire shapes (see mangaSearchAttrs' sibling types).
	AverageRating string `json:"averageRating"`
	// Synopsis is Kitsu's plain-text summary.
	Synopsis string `json:"synopsis"`
}

// posterImage mirrors Kitsu's posterImage size-variant map; only "original"
// is read (mirrors internal/metadata/kitsu's own shape).
type posterImage struct {
	Original string `json:"original"`
}

// mangaData is one JSON:API "manga" search-result resource.
type mangaData struct {
	ID         string           `json:"id"`
	Attributes mangaSearchAttrs `json:"attributes"`
}

// mangaCollectionResponse is the envelope Kitsu wraps a `GET /manga` search
// page in.
type mangaCollectionResponse struct {
	Data []mangaData `json:"data"`
}

// userResource is the bare subset of a JSON:API "users" resource this
// package reads (only the id, for self-lookup — see Client.selfUserID).
type userResource struct {
	ID string `json:"id"`
}

// userCollectionResponse is the envelope `GET /users?filter[self]=true`
// wraps its single-element result in.
type userCollectionResponse struct {
	Data []userResource `json:"data"`
}

// resourceRef is one bare JSON:API resource reference — a
// `{data:{id,type}}` relationship value, never inline attributes.
type resourceRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// relationshipRef wraps a single resourceRef under JSON:API's mandatory
// "data" key.
type relationshipRef struct {
	Data resourceRef `json:"data"`
}

// libraryEntryAttrs is the flat attribute bag on a "library-entries"
// resource this package reads/writes. RatingTwenty is Kitsu's native
// 0-20 rating scale (spec: "store native scale/codes; convert only at
// display" — this port never rescales it). A nil RatingTwenty means
// unrated, distinguishable from a genuine 0.
type libraryEntryAttrs struct {
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	RatingTwenty *int   `json:"ratingTwenty"`
	StartedAt    string `json:"startedAt,omitempty"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	Private      bool   `json:"private"`
}

// libraryEntryRelationships is the subset of a library-entry's
// `relationships` bag this package reads (the bound manga).
type libraryEntryRelationships struct {
	Manga relationshipRef `json:"manga"`
}

// libraryEntryData is one JSON:API "library-entries" resource, as returned
// by a GET/POST/PATCH.
type libraryEntryData struct {
	ID            string                    `json:"id"`
	Attributes    libraryEntryAttrs         `json:"attributes"`
	Relationships libraryEntryRelationships `json:"relationships"`
}

// libraryEntryCollectionResponse is the envelope
// `GET /library-entries?...` wraps its (0 or 1, filtered to one manga) page
// in.
type libraryEntryCollectionResponse struct {
	Data []libraryEntryData `json:"data"`
}

// libraryEntryResponse is the envelope a single POST/PATCH library-entries
// call returns.
type libraryEntryResponse struct {
	Data libraryEntryData `json:"data"`
}

// libraryEntryWriteRelationships is the request-side relationships shape a
// POST/PATCH /library-entries body carries: both the owning user and the
// bound manga must be named explicitly (JSON:API has no "current user"
// shortcut the way a REST API's Bearer-scoped write might).
type libraryEntryWriteRelationships struct {
	User  relationshipRef `json:"user"`
	Media relationshipRef `json:"media"`
}

// libraryEntryWriteData is the request-side "library-entries" resource body
// for a create (POST, no id) or update (PATCH, id required).
type libraryEntryWriteData struct {
	ID            string                         `json:"id,omitempty"`
	Type          string                         `json:"type"`
	Attributes    libraryEntryAttrs              `json:"attributes"`
	Relationships libraryEntryWriteRelationships `json:"relationships"`
}

// libraryEntryWriteRequest is the top-level JSON:API request envelope
// SaveEntry/UpdateEntry POST/PATCH.
type libraryEntryWriteRequest struct {
	Data libraryEntryWriteData `json:"data"`
}

// kitsuDateLayout is Kitsu's ISO-8601 date-time format for
// startedAt/finishedAt.
const kitsuDateLayout = time.RFC3339

// kitsuMangaURL builds Kitsu's canonical manga page URL from its slug, or ""
// when the slug is absent (mirrors Suwayomi-Server's/Komikku's KitsuApi.kt
// BASE_MANGA_URL, both on kitsu.app).
func kitsuMangaURL(slug string) string {
	if slug == "" {
		return ""
	}
	return "https://kitsu.app/manga/" + slug
}

// toTrackSearchResult maps one Kitsu manga search-page resource into the
// shared tracker.TrackSearchResult shape.
func toTrackSearchResult(d mangaData) tracker.TrackSearchResult {
	total := 0
	if d.Attributes.ChapterCount != nil {
		total = *d.Attributes.ChapterCount
	}
	return tracker.TrackSearchResult{
		RemoteID:      d.ID,
		Title:         d.Attributes.CanonicalTitle,
		URL:           kitsuMangaURL(d.Attributes.Slug),
		CoverURL:      d.Attributes.PosterImage.Original,
		Status:        d.Attributes.Status,
		TotalChapters: total,
		Type:          d.Attributes.Subtype,
		StartDate:     d.Attributes.StartDate,
		Score:         parseAverageRating(d.Attributes.AverageRating),
		Description:   d.Attributes.Synopsis,
	}
}

// parseAverageRating converts Kitsu's averageRating — a "0".."100" STRING
// percentage — into a float64 (mirrors internal/metadata/kitsu's own
// parseScore). An empty or unparseable value yields 0 (unknown), never an
// error — this is advisory display data, not a validated field.
func parseAverageRating(raw string) float64 {
	if raw == "" {
		return 0
	}
	rating, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return rating
}

// toTrackEntry maps one Kitsu library-entries resource to the shared
// tracker.TrackEntry shape. RemoteID comes from the entry's own manga
// relationship (always requested — see the client's field selections),
// never a caller-supplied fallback, so the result is self-consistent even
// for an UpdateEntry-by-library-id caller.
func toTrackEntry(d libraryEntryData) tracker.TrackEntry {
	score := 0.0
	if d.Attributes.RatingTwenty != nil {
		score = float64(*d.Attributes.RatingTwenty)
	}
	return tracker.TrackEntry{
		RemoteID:   d.Relationships.Manga.Data.ID,
		LibraryID:  d.ID,
		Status:     d.Attributes.Status,
		Score:      score,
		Progress:   float64(d.Attributes.Progress),
		Private:    d.Attributes.Private,
		StartDate:  parseKitsuDate(d.Attributes.StartedAt),
		FinishDate: parseKitsuDate(d.Attributes.FinishedAt),
	}
}

// buildLibraryEntryRequest builds the POST/PATCH /library-entries request
// body from a tracker.TrackEntry: id is "" for a create (SaveEntry) and the
// existing LibraryID for an update. RatingTwenty is omitted (nil) when
// entry.Score is 0 — Kitsu distinguishes "unrated" from a genuine 0, and
// this port never fabricates a rating the owner didn't set.
func buildLibraryEntryRequest(id string, entry tracker.TrackEntry, userID string) libraryEntryWriteRequest {
	var rating *int
	if entry.Score > 0 {
		r := int(entry.Score)
		rating = &r
	}
	return libraryEntryWriteRequest{
		Data: libraryEntryWriteData{
			ID:   id,
			Type: "library-entries",
			Attributes: libraryEntryAttrs{
				Status:       entry.Status,
				Progress:     int(entry.Progress),
				RatingTwenty: rating,
				StartedAt:    formatKitsuDate(entry.StartDate),
				FinishedAt:   formatKitsuDate(entry.FinishDate),
				Private:      entry.Private,
			},
			Relationships: libraryEntryWriteRelationships{
				User:  relationshipRef{Data: resourceRef{ID: userID, Type: "users"}},
				Media: relationshipRef{Data: resourceRef{ID: entry.RemoteID, Type: "manga"}},
			},
		},
	}
}

// parseKitsuDate parses Kitsu's ISO-8601 startedAt/finishedAt, returning nil
// for an empty or unparseable value — a malformed date must never crash a
// mapping, only degrade to "unknown" (mirrors mal.parseMALDate).
func parseKitsuDate(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(kitsuDateLayout, raw)
	if err != nil {
		return nil
	}
	return &t
}

// formatKitsuDate formats t as Kitsu's ISO-8601 date-time, or "" for a nil
// t (omitted from the request body via libraryEntryAttrs' `omitempty` tags).
func formatKitsuDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(kitsuDateLayout)
}
