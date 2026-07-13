package mangaupdates

import (
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// searchRequest is the JSON body POST /v1/series/search expects.
type searchRequest struct {
	Search  string `json:"search"`
	PerPage int    `json:"perpage"`
}

// searchResponse is the top-level envelope POST /v1/series/search returns.
type searchResponse struct {
	Results []searchResultEntry `json:"results"`
}

// searchResultEntry is one hit in a search response — MangaUpdates nests
// the actual series fields under "record" alongside per-hit metadata this
// provider doesn't use (hit_title, the caller's own user_list state, ...).
type searchResultEntry struct {
	Record searchRecord `json:"record"`
}

// searchRecord is the subset of a search hit's "record" object this
// provider reads. MangaUpdates ships more fields (type, rating_votes,
// last_updated, ...); anything not listed here is dropped by
// encoding/json automatically.
type searchRecord struct {
	SeriesID int64      `json:"series_id"`
	Title    string     `json:"title"`
	URL      string     `json:"url"`
	Image    coverImage `json:"image"`
	// Year is a free-text field (observed as a plain "2018", but
	// MangaUpdates' own client strips a trailing "-N" disambiguator before
	// parsing — see parseYear).
	Year string `json:"year"`
	// BayesianRating is a pointer so a series with no rating votes (field
	// absent/null) is distinguishable from a genuine 0.0 rating.
	BayesianRating *float64 `json:"bayesian_rating"`
}

// seriesDetail is the subset of GET /v1/series/{id}'s response this
// provider reads. MangaUpdates ships many more fields (publishers, anime,
// related_series, recommendations, rank, ...); anything not listed here is
// dropped by encoding/json automatically.
type seriesDetail struct {
	SeriesID    int64             `json:"series_id"`
	Title       string            `json:"title"`
	URL         string            `json:"url"`
	Associated  []associatedTitle `json:"associated"`
	Description string            `json:"description"`
	Image       coverImage        `json:"image"`
	Year        string            `json:"year"`
	Genres      []genreEntry      `json:"genres"`
	Categories  []categoryEntry   `json:"categories"`
	// Status is MangaUpdates' free-text release-schedule summary (e.g.
	// "200 Chapters + Prologue (Complete)\n..."), NOT a closed enum —
	// normalizeStatus derives metadata.SeriesMetadata.Status from Completed
	// primarily, falling back to a substring scan of this field only to
	// detect "hiatus".
	Status         string        `json:"status"`
	Completed      bool          `json:"completed"`
	Authors        []authorEntry `json:"authors"`
	BayesianRating *float64      `json:"bayesian_rating"`
}

// coverImage mirrors MangaUpdates' "image" object shape (shared by search
// records and series detail).
type coverImage struct {
	URL coverImageURL `json:"url"`
}

// coverImageURL holds the two size variants MangaUpdates serves; only
// Original is read (metadata.SeriesMetadata.CoverURL / SearchResult.CoverURL
// are single URLs, not a size set).
type coverImageURL struct {
	Original string `json:"original"`
	Thumb    string `json:"thumb"`
}

// genreEntry is one entry in a series' "genres" list.
type genreEntry struct {
	Genre string `json:"genre"`
}

// categoryEntry is one entry in a series' "categories" list — MangaUpdates'
// owner-curated tag vocabulary (distinct from Genres), mapped to
// metadata.SeriesMetadata.Tags.
type categoryEntry struct {
	Category string `json:"category"`
}

// associatedTitle is one entry in a series' "associated" alternate-title
// list. MangaUpdates does not label these by kind (unlike AniList's
// romaji/english/native/synonyms split) — every one maps to AltTitle Type
// SYNONYM.
type associatedTitle struct {
	Title string `json:"title"`
}

// authorEntry is one entry in a series' "authors" list. Type is
// MangaUpdates' own free-text credit ("Author" or "Artist" observed live)
// and is kept verbatim as metadata.Author.Role — metadata.Author.Role is
// documented as provider-defined, not a closed enum.
type authorEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// toAltTitles converts a series' associated-title list into
// metadata.AltTitle, all tagged SYNONYM per the associatedTitle doc comment.
// Blank titles are skipped.
func toAltTitles(assoc []associatedTitle) []metadata.AltTitle {
	if len(assoc) == 0 {
		return nil
	}
	out := make([]metadata.AltTitle, 0, len(assoc))
	for _, a := range assoc {
		if a.Title == "" {
			continue
		}
		out = append(out, metadata.AltTitle{Name: a.Title, Type: "SYNONYM"})
	}
	return out
}

// genreNames flattens a series' {genre}-object genre list into plain
// strings, skipping blanks.
func genreNames(genres []genreEntry) []string {
	if len(genres) == 0 {
		return nil
	}
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		if g.Genre == "" {
			continue
		}
		out = append(out, g.Genre)
	}
	return out
}

// categoryNames flattens a series' {category}-object category list into
// plain strings (mapped to metadata.SeriesMetadata.Tags), skipping blanks.
func categoryNames(cats []categoryEntry) []string {
	if len(cats) == 0 {
		return nil
	}
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		if c.Category == "" {
			continue
		}
		out = append(out, c.Category)
	}
	return out
}

// toAuthors converts a series' authors list to metadata.Author, keeping
// each entry's raw Type string verbatim as Role. Entries with no name are
// skipped.
func toAuthors(as []authorEntry) []metadata.Author {
	if len(as) == 0 {
		return nil
	}
	out := make([]metadata.Author, 0, len(as))
	for _, a := range as {
		if a.Name == "" {
			continue
		}
		out = append(out, metadata.Author{Name: a.Name, Role: a.Type})
	}
	return out
}

// toLinks builds the single MangaUpdates self-link from a series' url field
// (mirrors mangadex's "mu" link builder, from the other side). "" input
// yields nil output.
func toLinks(seriesURL string) []metadata.Link {
	if seriesURL == "" {
		return nil
	}
	return []metadata.Link{{Label: "MangaUpdates", URL: seriesURL}}
}

// parseYear parses MangaUpdates' free-text year field into an int, 0 on any
// unparseable input. MangaUpdates' own client strips a trailing "-N"
// disambiguator suffix before parsing (observed on some records) — ported
// here so "2018-1" parses as 2018, not a failure.
func parseYear(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if idx := strings.IndexByte(raw, '-'); idx > 0 {
		raw = raw[:idx]
	}
	y, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return y
}

// toScore rescales MangaUpdates' bayesian_rating (documented 0-10 scale) to
// metadata.SeriesMetadata.Score's normalized 0-100 scale. A nil rating
// (no votes yet) maps to 0 — "unknown", per SeriesMetadata's zero-value
// convention.
func toScore(rating *float64) float64 {
	if rating == nil {
		return 0
	}
	return *rating * 10
}

// normalizeStatus derives metadata.SeriesMetadata's normalized status
// vocabulary primarily from Completed (MangaUpdates' one structured
// signal): true always maps to "completed". When not completed, rawStatus
// — MangaUpdates' free-text release-schedule summary — is scanned
// case-insensitively for "hiatus"; anything else not-completed is
// "ongoing" (MangaUpdates has no separate "cancelled" flag this provider
// can read).
func normalizeStatus(rawStatus string, completed bool) string {
	if completed {
		return "completed"
	}
	if strings.Contains(strings.ToLower(rawStatus), "hiatus") {
		return "hiatus"
	}
	return "ongoing"
}

// toSeriesMetadata maps one MangaUpdates seriesDetail to
// metadata.SeriesMetadata.
func toSeriesMetadata(d seriesDetail) metadata.SeriesMetadata {
	return metadata.SeriesMetadata{
		Title:       d.Title,
		AltTitles:   toAltTitles(d.Associated),
		Description: d.Description,
		Status:      normalizeStatus(d.Status, d.Completed),
		Genres:      genreNames(d.Genres),
		Tags:        categoryNames(d.Categories),
		Authors:     toAuthors(d.Authors),
		Year:        parseYear(d.Year),
		Links:       toLinks(d.URL),
		Score:       toScore(d.BayesianRating),
		CoverURL:    d.Image.URL.Original,
	}
}

// toSearchResult maps one MangaUpdates search hit's record to
// metadata.SearchResult.
func toSearchResult(r searchRecord) metadata.SearchResult {
	return metadata.SearchResult{
		Provider: Key,
		RemoteID: strconv.FormatInt(r.SeriesID, 10),
		Title:    r.Title,
		URL:      r.URL,
		CoverURL: r.Image.URL.Original,
		Year:     parseYear(r.Year),
	}
}
