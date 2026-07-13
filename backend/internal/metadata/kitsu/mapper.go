package kitsu

import (
	"sort"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// mangaCollectionResponse is the envelope Kitsu wraps a `GET /manga` search
// page in. A search request never asks for `?include=categories`, so
// Included is never populated here — Search's result type
// (metadata.SearchResult) has no genre field to fill from it anyway.
type mangaCollectionResponse struct {
	Data []mangaData `json:"data"`
}

// mangaEntityResponse is the envelope Kitsu wraps a single `GET
// /manga/{id}` resource in. Included carries every resource pulled in by
// the request's `?include=` param — for GetSeriesMetadata that is always
// exactly the manga's categories, resolved by resolveGenres against
// Data.Relationships.Categories.Data.
type mangaEntityResponse struct {
	Data     mangaData        `json:"data"`
	Included []includedRecord `json:"included"`
}

// mangaData is one JSON:API "manga" resource: its id, the flat attribute
// bag, and its relationships (only Categories is read here; every other
// relationship Kitsu returns — genres, castings, staff, characters, ... —
// decodes away unread by encoding/json).
type mangaData struct {
	ID            string             `json:"id"`
	Attributes    mangaAttrs         `json:"attributes"`
	Relationships mangaRelationships `json:"relationships"`
}

// mangaRelationships is the subset of a manga resource's `relationships`
// bag this provider reads.
type mangaRelationships struct {
	Categories relationshipData `json:"categories"`
}

// relationshipData is a JSON:API to-many relationship's `data` array: bare
// {type,id} references, never inline attributes — the real resource
// (carrying `attributes.title`) arrives separately in the response's
// top-level `included` array (see resolveGenres).
type relationshipData struct {
	Data []resourceRef `json:"data"`
}

// resourceRef is one bare JSON:API resource reference.
type resourceRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// includedRecord is one entry of the JSON:API top-level `included` array —
// for this provider always a "categories" resource (the only relationship
// GetSeriesMetadata's `?include=` requests). Kitsu's manga categories
// double as its genre vocabulary here: there is no separate genre-name
// field reachable without a further relationship round-trip this provider
// deliberately does not make (see the package doc comment's "do not
// over-fetch" note, which also governs Authors).
type includedRecord struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes includedAttributes `json:"attributes"`
}

// includedAttributes is the subset of an included categories resource's
// attribute bag this provider reads.
type includedAttributes struct {
	Title string `json:"title"`
}

// posterImage is the subset of Kitsu's posterImage size-variant map this
// provider reads — only "original", the highest-resolution variant.
type posterImage struct {
	Original string `json:"original"`
}

// mangaAttrs is the subset of Kitsu's manga `attributes` bag this provider
// reads. Kitsu ships many more (ratingFrequencies, userCount,
// favoritesCount, ageRating, serialization, chapterCount, ...); anything
// not read here is dropped by encoding/json automatically.
type mangaAttrs struct {
	Slug     string `json:"slug"`
	Synopsis string `json:"synopsis"`
	// Titles maps locale keys ("en", "en_jp", "ja_jp", plus whatever else
	// Kitsu carries for a given series — "en_us", "en_kr", "ko_kr", ...) to
	// the title in that locale. Confirmed live (TestShapeKitsu): not every
	// series carries every key (Solo Leveling has no "en" entry at all).
	Titles            map[string]string `json:"titles"`
	CanonicalTitle    string            `json:"canonicalTitle"`
	AbbreviatedTitles []string          `json:"abbreviatedTitles"`
	// AverageRating is a "0".."100" STRING percentage (e.g. "85.07"), NOT a
	// JSON number — a Kitsu API quirk confirmed live via TestShapeKitsu.
	AverageRating string      `json:"averageRating"`
	StartDate     string      `json:"startDate"`
	Status        string      `json:"status"`
	PosterImage   posterImage `json:"posterImage"`
}

// altTitleLocale pairs one of Kitsu's three explicitly-labeled title-map
// locale keys with the metadata.AltTitle.Type the mission brief assigns it.
type altTitleLocale struct {
	locale string
	typ    string
}

// altTitleOrder is the deterministic emission order for the three
// explicitly-typed Kitsu title locales (per the mission brief: en ->
// LOCALIZED, en_jp -> ROMAJI, ja_jp -> NATIVE). Any OTHER locale key
// present in a series' titles map (e.g. "en_us", "en_kr", "ko_kr") is still
// emitted by buildAltTitles — as SYNONYM, in sorted-key order after these
// three — so no title data is silently dropped; Kitsu itself just does not
// distinguish those extra locales the way it does en/en_jp/ja_jp.
var altTitleOrder = []altTitleLocale{
	{"en", "LOCALIZED"},
	{"en_jp", "ROMAJI"},
	{"ja_jp", "NATIVE"},
}

// buildAltTitles converts Kitsu's `titles` locale map plus its flat
// `abbreviatedTitles` list into metadata.AltTitle values: the three
// explicitly-labeled locales in altTitleOrder first, then every OTHER
// locale key present (sorted for determinism — Go map iteration order is
// randomized) as SYNONYM, then every abbreviatedTitles entry as SYNONYM
// with no Lang (Kitsu's abbreviated titles carry no locale of their own).
// Blank names are skipped throughout so an AltTitle is never emitted for an
// absent slot.
func buildAltTitles(titles map[string]string, abbreviated []string) []metadata.AltTitle {
	out := make([]metadata.AltTitle, 0, len(titles)+len(abbreviated))
	labeled := make(map[string]bool, len(altTitleOrder))

	for _, ord := range altTitleOrder {
		labeled[ord.locale] = true
		name := titles[ord.locale]
		if name == "" {
			continue
		}
		out = append(out, metadata.AltTitle{Name: name, Type: ord.typ, Lang: ord.locale})
	}

	others := make([]string, 0, len(titles))
	for locale := range titles {
		if labeled[locale] {
			continue
		}
		others = append(others, locale)
	}
	sort.Strings(others)
	for _, locale := range others {
		name := titles[locale]
		if name == "" {
			continue
		}
		out = append(out, metadata.AltTitle{Name: name, Type: "SYNONYM", Lang: locale})
	}

	for _, name := range abbreviated {
		if name == "" {
			continue
		}
		out = append(out, metadata.AltTitle{Name: name, Type: "SYNONYM"})
	}

	return out
}

// parseYear extracts the first-publication year from Kitsu's startDate
// ("YYYY-MM-DD", e.g. "2018-03-04") by parsing the leading dash-delimited
// segment as an int. An empty or malformed date yields 0 (unknown), never
// an error — Year is advisory display data, not a validated field.
func parseYear(startDate string) int {
	if startDate == "" {
		return 0
	}
	head, _, _ := strings.Cut(startDate, "-")
	year, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return year
}

// parseScore converts Kitsu's averageRating — a "0".."100" STRING
// percentage (e.g. "85.07") — into the float64 metadata.SeriesMetadata.
// Score already expresses on a 0-100 scale, so this is pure string->float
// parsing with no rescaling. An empty or unparseable value yields 0
// (unknown).
func parseScore(averageRating string) float64 {
	if averageRating == "" {
		return 0
	}
	score, err := strconv.ParseFloat(averageRating, 64)
	if err != nil {
		return 0
	}
	return score
}

// mapStatus normalizes Kitsu's manga `status` enum into the shared
// metadata.SeriesMetadata.Status vocabulary. Kitsu's LIVE enum (confirmed
// via TestShapeKitsu against several real series, including a long-hiatus
// one) is current/finished/tba/unreleased/upcoming — there is no distinct
// "hiatus" value the way AniList has one (Hunter x Hunter, famously on a
// years-long hiatus, still reports "current"). "hiatus"/"cancelled"/
// "discontinued" are handled defensively in case Kitsu ever emits them
// (never observed live — flagged for a live re-check if this ever matters);
// tba/unreleased/upcoming (not yet publishing) and any unrecognized value
// map to "" (unknown), matching how internal/metadata/anilist's
// normalizeStatus treats AniList's own NOT_YET_RELEASED.
func mapStatus(status string) string {
	switch strings.ToLower(status) {
	case "current":
		return "ongoing"
	case "finished":
		return "completed"
	case "hiatus":
		return "hiatus"
	case "cancelled", "discontinued":
		return "cancelled"
	default:
		return ""
	}
}

// resolveGenres maps a manga's categories relationship
// (`relationships.categories.data[]`, bare {type,id} refs) to display names
// by looking each id up in the response's top-level `included` array (type
// "categories"), per the JSON:API convention Kitsu follows. A ref with no
// matching included record (not returned, or a non-"categories" type) is
// skipped rather than guessed at.
func resolveGenres(refs []resourceRef, included []includedRecord) []string {
	if len(refs) == 0 {
		return nil
	}

	titles := make(map[string]string, len(included))
	for _, inc := range included {
		if inc.Type != "categories" {
			continue
		}
		titles[inc.ID] = inc.Attributes.Title
	}

	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		title := titles[ref.ID]
		if title == "" {
			continue
		}
		out = append(out, title)
	}
	return out
}

// kitsuSeriesURL builds the Kitsu manga page URL from its slug, or "" when
// the slug is absent. Kitsu's manga resource has no external-reference
// `links` map the way MangaDex's does, so this self-referential page link
// is the only entry SeriesMetadata.Links ever carries for this provider.
func kitsuSeriesURL(slug string) string {
	if slug == "" {
		return ""
	}
	return "https://kitsu.io/manga/" + slug
}

// toSeriesMetadata maps one Kitsu manga resource (fetched via `GET
// /manga/{id}?include=categories`) plus its resolved included categories
// into the normalized metadata.SeriesMetadata contract.
//
// Authors is deliberately left empty: resolving Kitsu staff credits needs
// the separate `mangaStaff`/`staff` relationships (each requiring its own
// round-trip to expand castings -> people), and this provider's scope is a
// single detail fetch per mission brief's "do not over-fetch" instruction.
// A future task can add it as an opt-in second call.
func toSeriesMetadata(entity mangaEntityResponse) metadata.SeriesMetadata {
	d := entity.Data

	var links []metadata.Link
	if seriesURL := kitsuSeriesURL(d.Attributes.Slug); seriesURL != "" {
		links = []metadata.Link{{Label: "Kitsu", URL: seriesURL}}
	}

	return metadata.SeriesMetadata{
		Title:       d.Attributes.CanonicalTitle,
		AltTitles:   buildAltTitles(d.Attributes.Titles, d.Attributes.AbbreviatedTitles),
		Description: d.Attributes.Synopsis,
		Status:      mapStatus(d.Attributes.Status),
		Genres:      resolveGenres(d.Relationships.Categories.Data, entity.Included),
		Year:        parseYear(d.Attributes.StartDate),
		Score:       parseScore(d.Attributes.AverageRating),
		CoverURL:    d.Attributes.PosterImage.Original,
		Links:       links,
	}
}

// toSearchResult maps one Kitsu manga search-page resource into a
// metadata.SearchResult.
func toSearchResult(d mangaData) metadata.SearchResult {
	return metadata.SearchResult{
		Provider: Key,
		RemoteID: d.ID,
		Title:    d.Attributes.CanonicalTitle,
		URL:      kitsuSeriesURL(d.Attributes.Slug),
		CoverURL: d.Attributes.PosterImage.Original,
		Year:     parseYear(d.Attributes.StartDate),
	}
}
