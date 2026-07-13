package mal

import (
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// mainPicture mirrors MAL's `main_picture` object — {medium, large} URLs.
// Only Large is read (metadata.SeriesMetadata.CoverURL is a single URL, not
// a size set).
type mainPicture struct {
	Large string `json:"large"`
}

// mangaNode is the shape returned for one manga under the `fields` this
// package requests — shared by the search list entries (wrapped in {node:
// ...}) and, structurally, the subset of fields the detail response also
// carries (id/title/main_picture/start_date), so toSearchResult works
// identically regardless of which endpoint produced the node.
type mangaNode struct {
	ID          int         `json:"id"`
	Title       string      `json:"title"`
	MainPicture mainPicture `json:"main_picture"`
	StartDate   string      `json:"start_date"`
}

// mangaListEntry is one entry in a MAL search page — MAL wraps every list
// item in a `{"node": {...}}` envelope.
type mangaListEntry struct {
	Node mangaNode `json:"node"`
}

// mangaListResponse is the envelope `GET /manga?q=...` returns.
type mangaListResponse struct {
	Data []mangaListEntry `json:"data"`
}

// alternativeTitles mirrors MAL's `alternative_titles` object.
type alternativeTitles struct {
	Synonyms []string `json:"synonyms"`
	En       string   `json:"en"`
	Ja       string   `json:"ja"`
}

// mangaGenre is one entry in MAL's `genres` list.
type mangaGenre struct {
	Name string `json:"name"`
}

// authorNode mirrors MAL's `authors[].node` object — only the name parts
// this provider maps into metadata.Author.Name are requested.
type authorNode struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// mangaAuthor is one entry in MAL's `authors` list: a credited person plus
// their role on the work (e.g. "Story & Art").
type mangaAuthor struct {
	Node authorNode `json:"node"`
	Role string     `json:"role"`
}

// mangaDetail is the decoded shape of `GET /manga/{id}?fields=...` under
// detailFields. num_chapters and media_type are decoded for completeness
// but have no home in metadata.SeriesMetadata and are dropped by the
// mapper.
type mangaDetail struct {
	ID                int               `json:"id"`
	Title             string            `json:"title"`
	Synopsis          string            `json:"synopsis"`
	NumChapters       int               `json:"num_chapters"`
	Mean              float64           `json:"mean"`
	MainPicture       mainPicture       `json:"main_picture"`
	Status            string            `json:"status"`
	MediaType         string            `json:"media_type"`
	StartDate         string            `json:"start_date"`
	Genres            []mangaGenre      `json:"genres"`
	Authors           []mangaAuthor     `json:"authors"`
	AlternativeTitles alternativeTitles `json:"alternative_titles"`
}

// normalizeStatus maps MAL's manga status enum to metadata.SeriesMetadata's
// normalized status vocabulary. Any unrecognized status (including
// "not_yet_published", which metadata.SeriesMetadata has no dedicated slot
// for) maps to "" per this provider's documented contract.
func normalizeStatus(s string) string {
	switch s {
	case "currently_publishing":
		return "ongoing"
	case "finished":
		return "completed"
	case "on_hiatus":
		return "hiatus"
	default:
		return ""
	}
}

// yearFromStartDate extracts the 4-digit year prefix from a MAL start_date
// string ("YYYY", "YYYY-MM", or "YYYY-MM-DD" — MAL truncates the precision
// it has data for). Returns 0 for a blank or unparseable value.
func yearFromStartDate(startDate string) int {
	if len(startDate) < 4 {
		return 0
	}
	y, err := strconv.Atoi(startDate[:4])
	if err != nil {
		return 0
	}
	return y
}

// altTitles builds the AltTitle list from MAL's alternative_titles object:
// the named en/ja slots first (LOCALIZED/NATIVE, mirroring AniList's
// named-slots-before-list ordering), then every free-form synonym
// (SYNONYM). Blank slots are skipped so an AltTitle is never emitted for a
// field MAL left unset.
func altTitles(t alternativeTitles) []metadata.AltTitle {
	var out []metadata.AltTitle
	add := func(name, kind string) {
		if name == "" {
			return
		}
		out = append(out, metadata.AltTitle{Name: name, Type: kind})
	}

	add(t.En, "LOCALIZED")
	add(t.Ja, "NATIVE")
	for _, s := range t.Synonyms {
		add(s, "SYNONYM")
	}
	return out
}

// genreNames flattens MAL's {name}-object genre list into plain strings.
func genreNames(genres []mangaGenre) []string {
	if len(genres) == 0 {
		return nil
	}
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		out = append(out, g.Name)
	}
	return out
}

// authorName joins a MAL author node's first/last name, trimming any
// leading/trailing whitespace a blank half leaves behind (MAL sometimes
// carries only one of the two names, e.g. a single mononym stored as
// LastName with FirstName empty).
func authorName(n authorNode) string {
	return strings.TrimSpace(strings.TrimSpace(n.FirstName) + " " + strings.TrimSpace(n.LastName))
}

// authors converts MAL's authors list into metadata.Author, keeping each
// entry's raw role string verbatim (metadata.Author.Role is provider-
// defined free text, not a closed enum — see metadata.Author's doc
// comment). Entries with no usable name are skipped.
func authors(list []mangaAuthor) []metadata.Author {
	if len(list) == 0 {
		return nil
	}
	out := make([]metadata.Author, 0, len(list))
	for _, a := range list {
		name := authorName(a.Node)
		if name == "" {
			continue
		}
		out = append(out, metadata.Author{Name: name, Role: a.Role})
	}
	return out
}

// toSeriesMetadata maps a fully-fetched MAL manga detail (GET /manga/{id}
// under detailFields) into the normalized metadata.SeriesMetadata contract.
// Publisher and Tags are left at their zero values: MAL's field list this
// provider requests carries neither (see this package's doc comment).
func toSeriesMetadata(d mangaDetail) metadata.SeriesMetadata {
	return metadata.SeriesMetadata{
		Title:       d.Title,
		AltTitles:   altTitles(d.AlternativeTitles),
		Description: d.Synopsis,
		Status:      normalizeStatus(d.Status),
		Genres:      genreNames(d.Genres),
		Authors:     authors(d.Authors),
		Year:        yearFromStartDate(d.StartDate),
		Score:       d.Mean * 10,
		CoverURL:    d.MainPicture.Large,
	}
}

// mangaURL builds a MAL manga page URL from its numeric id — MAL's REST API
// carries no canonical-URL field the way AniList's siteUrl does, so this
// provider constructs it from the well-known, stable URL shape.
func mangaURL(id int) string {
	return "https://myanimelist.net/manga/" + strconv.Itoa(id)
}

// toSearchResult maps one MAL manga node (from a search list entry) to a
// metadata.SearchResult.
func toSearchResult(n mangaNode) metadata.SearchResult {
	return metadata.SearchResult{
		Provider: Key,
		RemoteID: strconv.Itoa(n.ID),
		Title:    n.Title,
		URL:      mangaURL(n.ID),
		CoverURL: n.MainPicture.Large,
		Year:     yearFromStartDate(n.StartDate),
	}
}
