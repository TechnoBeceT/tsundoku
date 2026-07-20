package anilist

import (
	"strconv"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// seriesFieldSelection is the AniList Media field set both the search
// (Page.media) and single-lookup (Media(id)) queries select — one shared
// selection so the two entry points decode into the identical gqlMedia
// shape and the mapper logic never forks (mirrors suwayomi's
// gqlMangaNode/mangaFieldSelection reuse across its own Search/Browse/
// MangaMeta operations — see the repo architecture notes). Fields chosen per
// brief/komf-metadata-engine-reference's documented AniList field list,
// confirmed live against the real API before this file was written
// (TestShapeAniList).
const seriesFieldSelection = `
	id
	title { romaji english native }
	status
	description
	genres
	synonyms
	tags { name }
	staff { edges { role node { name { full } } } }
	startDate { year }
	averageScore
	coverImage { extraLarge }
	externalLinks { site url }
	siteUrl
`

// searchQuery is AniList's paginated fuzzy-search entry point.
const searchQuery = `
query ($search: String, $perPage: Int) {
	Page(page: 1, perPage: $perPage) {
		media(search: $search, type: MANGA) {` + seriesFieldSelection + `
		}
	}
}
`

// byIDQuery is AniList's single-record lookup by numeric Media id.
const byIDQuery = `
query ($id: Int) {
	Media(id: $id, type: MANGA) {` + seriesFieldSelection + `
	}
}
`

// gqlTitle mirrors AniList's MediaTitle selection.
type gqlTitle struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

// gqlDate mirrors AniList's FuzzyDate selection (only Year is requested —
// month/day have no home in metadata.SeriesMetadata).
type gqlDate struct {
	Year int `json:"year"`
}

// gqlTag mirrors one AniList MediaTag's requested field.
type gqlTag struct {
	Name string `json:"name"`
}

// gqlStaffName mirrors AniList's StaffName selection.
type gqlStaffName struct {
	Full string `json:"full"`
}

// gqlStaffNode mirrors one AniList Staff node's requested field.
type gqlStaffNode struct {
	Name gqlStaffName `json:"name"`
}

// gqlStaffEdge mirrors one AniList StaffEdge: the credited role plus the
// staff node it points at.
type gqlStaffEdge struct {
	Role string       `json:"role"`
	Node gqlStaffNode `json:"node"`
}

// gqlStaffConnection mirrors AniList's StaffConnection selection.
type gqlStaffConnection struct {
	Edges []gqlStaffEdge `json:"edges"`
}

// gqlCoverImage mirrors AniList's MediaCoverImage selection (only the
// largest size is requested — metadata.SeriesMetadata.CoverURL is a single
// URL, not a size set).
type gqlCoverImage struct {
	ExtraLarge string `json:"extraLarge"`
}

// gqlExternalLink mirrors one AniList MediaExternalLink's requested fields.
type gqlExternalLink struct {
	Site string `json:"site"`
	URL  string `json:"url"`
}

// gqlMedia is the decoded shape of one AniList Media node under
// seriesFieldSelection — shared by both searchPageData and mediaData so
// toSeriesMetadata/toSearchResult work identically regardless of which
// query produced the node.
type gqlMedia struct {
	ID            int                `json:"id"`
	Title         gqlTitle           `json:"title"`
	Status        string             `json:"status"`
	Description   string             `json:"description"`
	Genres        []string           `json:"genres"`
	Synonyms      []string           `json:"synonyms"`
	Tags          []gqlTag           `json:"tags"`
	Staff         gqlStaffConnection `json:"staff"`
	StartDate     gqlDate            `json:"startDate"`
	AverageScore  int                `json:"averageScore"`
	CoverImage    gqlCoverImage      `json:"coverImage"`
	ExternalLinks []gqlExternalLink  `json:"externalLinks"`
	SiteURL       string             `json:"siteUrl"`
}

// searchPageData is the "data" payload of searchQuery.
type searchPageData struct {
	Page struct {
		Media []gqlMedia `json:"media"`
	} `json:"Page"`
}

// mediaData is the "data" payload of byIDQuery.
type mediaData struct {
	Media gqlMedia `json:"Media"`
}

// preferredTitle returns t's display title: the English localized title
// when AniList provides one, else the romanized title (AniList's own
// fallback — Native is never used as the primary display title, only ever
// surfaced via AltTitles).
func preferredTitle(t gqlTitle) string {
	if t.English != "" {
		return t.English
	}
	return t.Romaji
}

// normalizeStatus maps AniList's MediaStatus enum to metadata.
// SeriesMetadata's normalized status vocabulary. NOT_YET_RELEASED (and any
// future/unrecognized AniList status) maps to "" — metadata.SeriesMetadata.
// Status only has slots for ongoing/completed/hiatus/cancelled.
func normalizeStatus(s string) string {
	switch s {
	case "RELEASING":
		return "ongoing"
	case "FINISHED":
		return "completed"
	case "HIATUS":
		return "hiatus"
	case "CANCELLED":
		return "cancelled"
	default:
		return ""
	}
}

// altTitles builds the AltTitle list from a gqlTitle's three named slots
// plus the free-form synonyms list, per brief/komf-metadata-engine-
// reference's mapping (romaji=ROMAJI, english=LOCALIZED, native=NATIVE,
// synonyms=SYNONYM). Blank slots are skipped so an AltTitle is never
// emitted for a field AniList left unset.
func altTitles(t gqlTitle, synonyms []string) []metadata.AltTitle {
	var out []metadata.AltTitle
	add := func(name, kind string) {
		if name == "" {
			return
		}
		out = append(out, metadata.AltTitle{Name: name, Type: kind})
	}

	add(t.Romaji, "ROMAJI")
	add(t.English, "LOCALIZED")
	add(t.Native, "NATIVE")
	for _, s := range synonyms {
		add(s, "SYNONYM")
	}
	return out
}

// authors converts AniList's staff edges into metadata.Author, keeping each
// edge's raw role string verbatim: AniList's roles ("Story", "Art",
// "Original Story", "Story (chs 1-92)", ...) are already human-readable
// free text, and metadata.Author.Role is documented as provider-defined
// (not a closed enum) — normalizing it would lose information AniList
// itself curates. Entries with no staff name are skipped.
func authors(staff gqlStaffConnection) []metadata.Author {
	if len(staff.Edges) == 0 {
		return nil
	}
	out := make([]metadata.Author, 0, len(staff.Edges))
	for _, e := range staff.Edges {
		if e.Node.Name.Full == "" {
			continue
		}
		out = append(out, metadata.Author{Name: e.Node.Name.Full, Role: e.Role})
	}
	return out
}

// tagNames flattens AniList's {name}-object tag list into plain strings.
func tagNames(tags []gqlTag) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Name)
	}
	return out
}

// mapLinks converts AniList's externalLinks into metadata.Link (site→Label,
// url→URL).
func mapLinks(el []gqlExternalLink) []metadata.Link {
	if len(el) == 0 {
		return nil
	}
	out := make([]metadata.Link, 0, len(el))
	for _, l := range el {
		out = append(out, metadata.Link{Label: l.Site, URL: l.URL})
	}
	return out
}

// toSeriesMetadata maps one AniList Media node to metadata.SeriesMetadata.
// Genres is copied (not aliased) so a caller mutating the returned slice can
// never corrupt anything shared with the decoded JSON structure.
func toSeriesMetadata(m gqlMedia) metadata.SeriesMetadata {
	return metadata.SeriesMetadata{
		Title:       preferredTitle(m.Title),
		AltTitles:   altTitles(m.Title, m.Synonyms),
		Description: m.Description,
		Status:      normalizeStatus(m.Status),
		Genres:      append([]string(nil), m.Genres...),
		Tags:        tagNames(m.Tags),
		Authors:     authors(m.Staff),
		Year:        m.StartDate.Year,
		Links:       mapLinks(m.ExternalLinks),
		Score:       float64(m.AverageScore),
		CoverURL:    m.CoverImage.ExtraLarge,
	}
}

// toSearchResult maps one AniList Media node to metadata.SearchResult.
func toSearchResult(m gqlMedia) metadata.SearchResult {
	return metadata.SearchResult{
		Provider: providerKey,
		RemoteID: strconv.Itoa(m.ID),
		Title:    preferredTitle(m.Title),
		URL:      m.SiteURL,
		CoverURL: m.CoverImage.ExtraLarge,
		Year:     m.StartDate.Year,
	}
}
