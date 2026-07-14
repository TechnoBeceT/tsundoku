package anilist

import (
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// Wire response shapes — one struct per GraphQL selection this client
// issues (queries.go). Kept separate from client.go per the codebase's
// "one thing per file" convention (see internal/metadata/anilist/mapper.go
// for the mirrored split).

type titleData struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

type coverImageData struct {
	Large string `json:"large"`
}

type mediaSearchItem struct {
	ID         int            `json:"id"`
	Title      titleData      `json:"title"`
	CoverImage coverImageData `json:"coverImage"`
	Status     string         `json:"status"`
	Chapters   *int           `json:"chapters"`
	SiteURL    string         `json:"siteUrl"`
	// Format is AniList's MediaFormat enum (e.g. "MANGA", "NOVEL",
	// "ONE_SHOT") — Search-Enrichment addition, see queries.go's
	// searchQuery doc comment.
	Format string `json:"format"`
	// StartDate only carries Year on a search hit — see searchQuery.
	StartDate fuzzyDate `json:"startDate"`
	// AverageScore is AniList's RAW 0-100 community average; nil when
	// AniList has no rating data yet for this manga.
	AverageScore *int `json:"averageScore"`
	// Description is plain text (the query requests asHtml:false).
	Description string `json:"description"`
}

type searchPageData struct {
	Page struct {
		Media []mediaSearchItem `json:"media"`
	} `json:"Page"`
}

type mediaListOptionsData struct {
	ScoreFormat string `json:"scoreFormat"`
}

type viewerData struct {
	Viewer *struct {
		ID               int                  `json:"id"`
		Name             string               `json:"name"`
		MediaListOptions mediaListOptionsData `json:"mediaListOptions"`
	} `json:"Viewer"`
}

type fuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

type mediaListEntry struct {
	ID          int       `json:"id"`
	MediaID     int       `json:"mediaId"`
	Status      string    `json:"status"`
	Score       float64   `json:"score"`
	Progress    int       `json:"progress"`
	Private     bool      `json:"private"`
	StartedAt   fuzzyDate `json:"startedAt"`
	CompletedAt fuzzyDate `json:"completedAt"`
}

type getEntryData struct {
	MediaList *mediaListEntry `json:"MediaList"`
}

type saveEntryData struct {
	SaveMediaListEntry *mediaListEntry `json:"SaveMediaListEntry"`
}

type deleteEntryData struct {
	DeleteMediaListEntry *struct {
		Deleted bool `json:"deleted"`
	} `json:"DeleteMediaListEntry"`
}

// toTrackSearchResult maps one AniList search hit to the shared
// tracker.TrackSearchResult shape.
func toTrackSearchResult(m mediaSearchItem) tracker.TrackSearchResult {
	total := 0
	if m.Chapters != nil {
		total = *m.Chapters
	}
	score := 0.0
	if m.AverageScore != nil {
		score = float64(*m.AverageScore)
	}
	startDate := ""
	if m.StartDate.Year != nil && *m.StartDate.Year != 0 {
		startDate = strconv.Itoa(*m.StartDate.Year)
	}
	return tracker.TrackSearchResult{
		RemoteID:      strconv.Itoa(m.ID),
		Title:         bestTitle(m.Title),
		URL:           m.SiteURL,
		CoverURL:      m.CoverImage.Large,
		Status:        m.Status,
		TotalChapters: total,
		Type:          m.Format,
		StartDate:     startDate,
		Score:         score,
		Description:   m.Description,
	}
}

// bestTitle picks the most useful display title from AniList's three title
// variants: English first (most owners read English titles), then Romaji,
// then Native — never returns "" if any variant is non-empty.
func bestTitle(t titleData) string {
	if t.English != "" {
		return t.English
	}
	if t.Romaji != "" {
		return t.Romaji
	}
	return t.Native
}

// toTrackEntry maps one AniList MediaList entry to the shared
// tracker.TrackEntry shape. RemoteID comes from the entry's own mediaId
// field (always selected — see mediaListEntrySelection), never a
// caller-supplied fallback, so the result is self-consistent even when the
// caller only had a LibraryID handy (UpdateEntry/GetEntry-by-id callers).
func toTrackEntry(e *mediaListEntry) tracker.TrackEntry {
	return tracker.TrackEntry{
		RemoteID:   strconv.Itoa(e.MediaID),
		LibraryID:  strconv.Itoa(e.ID),
		Status:     e.Status,
		Score:      e.Score,
		Progress:   float64(e.Progress),
		Private:    e.Private,
		StartDate:  fuzzyDateToTime(e.StartedAt),
		FinishDate: fuzzyDateToTime(e.CompletedAt),
	}
}

// fuzzyDateToTime converts an AniList FuzzyDate to a *time.Time, or nil when
// the year is unset (AniList represents "no date" as a FuzzyDate with all
// null components, never a null FuzzyDate itself). A fuzzy date with a year
// but no month/day defaults the missing components to 1 (January 1st of
// that year) rather than rejecting the value — better an approximate date
// than a dropped one.
func fuzzyDateToTime(d fuzzyDate) *time.Time {
	if d.Year == nil || *d.Year == 0 {
		return nil
	}
	month := 1
	if d.Month != nil && *d.Month != 0 {
		month = *d.Month
	}
	day := 1
	if d.Day != nil && *d.Day != 0 {
		day = *d.Day
	}
	t := time.Date(*d.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return &t
}

// fuzzyDateInput is the wire shape AniList's FuzzyDateInput expects for a
// mutation VARIABLE (as opposed to fuzzyDate, the shape a QUERY RESPONSE
// returns) — same fields, kept as a separate type so a future divergence in
// either direction (e.g. the input type gaining a required field) doesn't
// silently cross-contaminate the other.
type fuzzyDateInput struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// timeToFuzzyDateInput converts a *time.Time to AniList's FuzzyDateInput
// wire shape, or nil when t is nil (AniList treats an absent/null
// FuzzyDateInput variable as "leave this date unset").
func timeToFuzzyDateInput(t *time.Time) *fuzzyDateInput {
	if t == nil {
		return nil
	}
	return &fuzzyDateInput{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
}
