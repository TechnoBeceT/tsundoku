package mangadex

import (
	"sort"
	"strings"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// mangaEntityResponse is the envelope MangaDex wraps a single `GET
// /manga/{id}` result in.
type mangaEntityResponse struct {
	Data mangaData `json:"data"`
}

// mangaCollectionResponse is the envelope MangaDex wraps a `GET /manga`
// search page in.
type mangaCollectionResponse struct {
	Data []mangaData `json:"data"`
}

// mangaData is one manga resource: its id, the flat attribute bag, and the
// (optionally include-expanded) relationships to author/artist/cover_art.
type mangaData struct {
	ID            string         `json:"id"`
	Attributes    mangaAttrs     `json:"attributes"`
	Relationships []relationship `json:"relationships"`
}

// mangaAttrs is the subset of MangaDex's manga `attributes` bag this
// provider reads. MangaDex ships many more fields (contentRating,
// publicationDemographic, ...); anything not read here is dropped by
// encoding/json automatically.
type mangaAttrs struct {
	// Title maps BCP-47-ish locale keys ("en", "ja-ro", ...) to the title
	// in that locale. MangaDex titles are NOT globally guaranteed to carry
	// an "en" entry (see One Piece: only "ja-ro").
	Title map[string]string `json:"title"`
	// AltTitles is a list of single-entry locale maps, one per alt title
	// (MangaDex's own shape — NOT one map with every locale).
	AltTitles        []map[string]string `json:"altTitles"`
	Description      map[string]string   `json:"description"`
	Status           string              `json:"status"`
	Year             int                 `json:"year"`
	Tags             []mangaTag          `json:"tags"`
	Links            map[string]string   `json:"links"`
	OriginalLanguage string              `json:"originalLanguage"`
}

// mangaTag is one MangaDex tag resource. Group "genre" maps to
// SeriesMetadata.Genres; every other group ("theme", "format", "content",
// ...) maps to Tags.
type mangaTag struct {
	Attributes struct {
		Name  map[string]string `json:"name"`
		Group string            `json:"group"`
	} `json:"attributes"`
}

// relationship is one entry in a manga's `relationships` array. Attributes
// is only populated when the request asked for that relationship type via
// `includes[]=<type>` — an un-included relationship arrives as a bare
// {id,type} reference, so Attributes is nil and must be guarded.
type relationship struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Attributes *relationshipAttributes `json:"attributes,omitempty"`
}

// relationshipAttributes carries only the fields this provider reads off
// an included author/artist/cover_art relationship. MangaDex's real
// payload carries many more (biography, social links, ...); they decode
// away silently.
type relationshipAttributes struct {
	// Name is set on author/artist relationships.
	Name string `json:"name,omitempty"`
	// FileName/Volume/Locale are set on a cover_art relationship.
	FileName string `json:"fileName,omitempty"`
	Volume   string `json:"volume,omitempty"`
	Locale   string `json:"locale,omitempty"`
}

// coverCollectionResponse is the envelope MangaDex wraps a `GET /cover`
// page in.
type coverCollectionResponse struct {
	Data []coverData `json:"data"`
}

// coverData is one MangaDex cover_art resource returned by `GET /cover`.
type coverData struct {
	Attributes struct {
		FileName string `json:"fileName"`
		Volume   string `json:"volume"`
		Locale   string `json:"locale"`
	} `json:"attributes"`
}

// pickTitle chooses the display title from a MangaDex locale->title map:
// prefer "en", else fall back to the LEXICOGRAPHICALLY SMALLEST locale key
// so the choice is deterministic (Go map iteration order is randomized,
// and MangaDex titles are not guaranteed to carry an "en" entry — e.g.
// One Piece's title map holds only "ja-ro").
func pickTitle(titles map[string]string) string {
	if v, ok := titles["en"]; ok && v != "" {
		return v
	}
	if len(titles) == 0 {
		return ""
	}
	keys := make([]string, 0, len(titles))
	for k := range titles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return titles[keys[0]]
}

// altTitleType infers the metadata.AltTitle.Type vocabulary (ROMAJI,
// NATIVE, LOCALIZED, SYNONYM) from a MangaDex alt-title locale key, since
// MangaDex itself does not label alt titles by kind the way AniList does.
func altTitleType(locale, originalLanguage string) string {
	switch {
	case strings.HasSuffix(locale, "-ro"):
		return "ROMAJI"
	case locale == originalLanguage:
		return "NATIVE"
	case locale == "en":
		return "LOCALIZED"
	default:
		return "SYNONYM"
	}
}

// toAltTitles flattens MangaDex's list-of-single-entry-locale-maps
// altTitles shape into metadata.AltTitle values, inferring Type per
// altTitleType. Blank names are skipped.
func toAltTitles(raw []map[string]string, originalLanguage string) []metadata.AltTitle {
	out := make([]metadata.AltTitle, 0, len(raw))
	for _, m := range raw {
		for lang, name := range m {
			if name == "" {
				continue
			}
			out = append(out, metadata.AltTitle{
				Name: name,
				Type: altTitleType(lang, originalLanguage),
				Lang: lang,
			})
		}
	}
	return out
}

// splitTags partitions MangaDex tags into Genres (group "genre") and Tags
// (every other group), reading the English tag name. A tag with no
// English name is skipped rather than guessed at.
func splitTags(tags []mangaTag) (genres, others []string) {
	for _, t := range tags {
		name := t.Attributes.Name["en"]
		if name == "" {
			continue
		}
		if t.Attributes.Group == "genre" {
			genres = append(genres, name)
		} else {
			others = append(others, name)
		}
	}
	return genres, others
}

// toAuthors maps author/artist relationships to metadata.Author, using the
// WRITER/ARTIST roles from the same vocabulary provider.go documents.
// Relationships without expanded Attributes (not requested via includes[])
// are skipped rather than emitting a nameless author.
func toAuthors(rels []relationship) []metadata.Author {
	var out []metadata.Author
	for _, r := range rels {
		if r.Attributes == nil || r.Attributes.Name == "" {
			continue
		}
		switch r.Type {
		case "author":
			out = append(out, metadata.Author{Name: r.Attributes.Name, Role: "WRITER"})
		case "artist":
			out = append(out, metadata.Author{Name: r.Attributes.Name, Role: "ARTIST"})
		}
	}
	return out
}

// findCoverFileName returns the fileName off an expanded cover_art
// relationship, or "" when none is present (not requested, or the manga
// has no cover).
func findCoverFileName(rels []relationship) string {
	for _, r := range rels {
		if r.Type == "cover_art" && r.Attributes != nil {
			return r.Attributes.FileName
		}
	}
	return ""
}

// coverURL builds the .512.jpg thumbnail URL MangaDex serves cover art
// from, given the owning manga id and the cover's stored file name. ""
// input yields "" output — never a malformed URL.
func coverURL(mangaID, fileName string) string {
	if fileName == "" {
		return ""
	}
	return uploadsBaseURL + "/covers/" + mangaID + "/" + fileName + ".512.jpg"
}

// linkOrder is the deterministic emission order for toLinks — MangaDex's
// `links` field is a map (random Go iteration order), so a fixed key
// sequence keeps SeriesMetadata.Links stable across calls.
var linkOrder = []string{"mal", "al", "ap", "bw", "mu", "nu", "kt", "amz", "ebj", "cdj", "raw", "engtl"}

// linkBuild describes how to turn one MangaDex `links` map value into a
// full metadata.Link for a known key.
type linkBuild struct {
	label string
	build func(v string) string
}

func identityLink(v string) string { return v }

// linkBuilders maps MangaDex's documented external-link keys to a display
// label and a URL builder. Keys carrying a bare id/slug get a builder that
// prefixes the canonical site URL; keys MangaDex already stores as a full
// URL (amz/ebj/cdj/raw/engtl) pass through unchanged. Keys with no known
// builder (e.g. undocumented "bl") are simply never emitted by toLinks —
// safer than guessing a URL shape that might not resolve.
var linkBuilders = map[string]linkBuild{
	"mal":   {"MyAnimeList", func(v string) string { return "https://myanimelist.net/manga/" + v }},
	"al":    {"AniList", func(v string) string { return "https://anilist.co/manga/" + v }},
	"ap":    {"Anime-Planet", func(v string) string { return "https://www.anime-planet.com/manga/" + v }},
	"bw":    {"BookWalker", func(v string) string { return "https://bookwalker.jp/" + v }},
	"mu":    {"MangaUpdates", func(v string) string { return "https://www.mangaupdates.com/series/" + v }},
	"nu":    {"NovelUpdates", func(v string) string { return "https://www.novelupdates.com/series/" + v }},
	"kt":    {"Kitsu", func(v string) string { return "https://kitsu.io/manga/" + v }},
	"amz":   {"Amazon", identityLink},
	"ebj":   {"eBookJapan", identityLink},
	"cdj":   {"CDJapan", identityLink},
	"raw":   {"Raw", identityLink},
	"engtl": {"Official English", identityLink},
}

// toLinks converts a MangaDex `links` map into metadata.Link values in
// linkOrder, dropping unknown keys and empty values.
func toLinks(raw map[string]string) []metadata.Link {
	var out []metadata.Link
	for _, key := range linkOrder {
		v, ok := raw[key]
		if !ok || v == "" {
			continue
		}
		b, ok := linkBuilders[key]
		if !ok {
			continue
		}
		out = append(out, metadata.Link{Label: b.label, URL: b.build(v)})
	}
	return out
}

// toSeriesMetadata maps one fully-expanded MangaDex mangaData (fetched via
// GET /manga/{id}?includes[]=author&includes[]=artist&includes[]=cover_art)
// into the normalized metadata.SeriesMetadata contract. Score and
// Publisher are left at their zero values: MangaDex's manga resource
// carries neither (rating lives behind a separate /statistics endpoint,
// out of this task's scope).
func toSeriesMetadata(id string, d mangaData) metadata.SeriesMetadata {
	genres, tags := splitTags(d.Attributes.Tags)
	return metadata.SeriesMetadata{
		Title:       pickTitle(d.Attributes.Title),
		AltTitles:   toAltTitles(d.Attributes.AltTitles, d.Attributes.OriginalLanguage),
		Description: d.Attributes.Description["en"],
		Status:      strings.ToLower(d.Attributes.Status),
		Genres:      genres,
		Tags:        tags,
		Authors:     toAuthors(d.Relationships),
		Year:        d.Attributes.Year,
		Links:       toLinks(d.Attributes.Links),
		CoverURL:    coverURL(id, findCoverFileName(d.Relationships)),
	}
}

// toSearchResult maps one MangaDex search-page mangaData into a
// metadata.SearchResult.
func toSearchResult(d mangaData) metadata.SearchResult {
	return metadata.SearchResult{
		Provider: Key,
		RemoteID: d.ID,
		Title:    pickTitle(d.Attributes.Title),
		URL:      "https://mangadex.org/title/" + d.ID,
		CoverURL: coverURL(d.ID, findCoverFileName(d.Relationships)),
		Year:     d.Attributes.Year,
	}
}

// coverLabel builds a short, human-readable label for a cover gallery
// entry from its volume + locale (e.g. "Vol. 115 (ja)"), falling back
// gracefully when either is absent.
func coverLabel(volume, locale string) string {
	switch {
	case volume != "" && locale != "":
		return "Vol. " + volume + " (" + locale + ")"
	case volume != "":
		return "Vol. " + volume
	case locale != "":
		return "(" + locale + ")"
	default:
		return ""
	}
}
