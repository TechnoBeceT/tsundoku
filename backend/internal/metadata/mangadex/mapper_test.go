// Package mangadex mapper tests run white-box (package mangadex, not
// mangadex_test) because toSeriesMetadata/toSearchResult/pickTitle/etc.
// are unexported mapping internals with no public equivalent — there is
// no network-free way to exercise them through the exported Client
// without an injectable base URL this task's scope doesn't call for.
// Every fixture here was captured from a REAL api.mangadex.org response
// (see shape_test.go) and trimmed only in array LENGTH (fewer alt titles,
// fewer search hits, fewer gallery covers) — never in field shape.
package mangadex

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// loadFixture reads and JSON-decodes a captured testdata fixture into out,
// failing the test on any I/O or decode error.
func loadFixture(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-only; path is a literal testdata/*.json fixture
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
}

// loadOnePiece maps the captured One Piece manga-detail fixture (GET
// /manga/{id}?includes[]=author&includes[]=artist&includes[]=cover_art)
// once per subtest, so each field's assertions stay a small, independently
// readable (and independently cognitive-complexity-scored) test.
func loadOnePiece(t *testing.T) metadata.SeriesMetadata {
	t.Helper()
	var entity mangaEntityResponse
	loadFixture(t, "testdata/manga_detail.json", &entity)
	return toSeriesMetadata(entity.Data.ID, entity.Data)
}

// TestToSeriesMetadata_OnePiece_Scalars asserts the plain scalar fields —
// Title falls back to "ja-ro" since the fixture carries no "en" title key.
func TestToSeriesMetadata_OnePiece_Scalars(t *testing.T) {
	got := loadOnePiece(t)

	if got.Title != "One Piece" {
		t.Errorf("Title = %q, want %q", got.Title, "One Piece")
	}
	if got.Status != "ongoing" {
		t.Errorf("Status = %q, want %q", got.Status, "ongoing")
	}
	if got.Year != 1997 {
		t.Errorf("Year = %d, want 1997", got.Year)
	}
	if got.Description == "" {
		t.Error("Description is empty, want the English synopsis")
	}
}

// TestToSeriesMetadata_OnePiece_TagsSplitByGroup asserts tags are
// partitioned by MangaDex's `group` attribute: "genre" -> Genres,
// everything else (here "format" and "content") -> Tags.
func TestToSeriesMetadata_OnePiece_TagsSplitByGroup(t *testing.T) {
	got := loadOnePiece(t)

	wantGenres := map[string]bool{"Sci-Fi": true, "Action": true, "Comedy": true, "Adventure": true, "Drama": true, "Fantasy": true}
	if len(got.Genres) != len(wantGenres) {
		t.Errorf("Genres = %v, want %d entries matching %v", got.Genres, len(wantGenres), wantGenres)
	}
	for _, g := range got.Genres {
		if !wantGenres[g] {
			t.Errorf("Genres contains unexpected %q (want only genre-group tags)", g)
		}
	}

	wantTags := map[string]bool{"Award Winning": true, "Gore": true}
	if len(got.Tags) != len(wantTags) {
		t.Errorf("Tags = %v, want %d entries matching %v", got.Tags, len(wantTags), wantTags)
	}
	for _, tag := range got.Tags {
		if !wantTags[tag] {
			t.Errorf("Tags contains unexpected %q (want only non-genre-group tags)", tag)
		}
	}
}

// TestToSeriesMetadata_OnePiece_Authors asserts the author/artist
// relationships both surface — One Piece's author and artist are the same
// person, and the mapper must emit BOTH roles, not dedupe across role.
func TestToSeriesMetadata_OnePiece_Authors(t *testing.T) {
	got := loadOnePiece(t)

	wantAuthors := []metadata.Author{
		{Name: "Oda Eiichirou (尾田栄一郎)", Role: "WRITER"},
		{Name: "Oda Eiichirou (尾田栄一郎)", Role: "ARTIST"},
	}
	if len(got.Authors) != len(wantAuthors) {
		t.Fatalf("Authors = %+v, want %+v", got.Authors, wantAuthors)
	}
	for i, want := range wantAuthors {
		if got.Authors[i] != want {
			t.Errorf("Authors[%d] = %+v, want %+v", i, got.Authors[i], want)
		}
	}
}

// TestToSeriesMetadata_OnePiece_CoverURL asserts CoverURL is built from
// the manga id + the cover_art relationship's fileName, at the .512.jpg
// thumbnail size.
func TestToSeriesMetadata_OnePiece_CoverURL(t *testing.T) {
	got := loadOnePiece(t)

	wantCover := "https://uploads.mangadex.org/covers/a1c7c817-4e59-43b7-9365-09675a149a6f/2f4aca53-64c7-46ac-ae85-3bc9b3169890.png.512.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
}

// TestToSeriesMetadata_OnePiece_Links asserts only keys with a known
// builder are emitted, in linkOrder; "bl" (undocumented, no builder) must
// be dropped rather than guessed at.
func TestToSeriesMetadata_OnePiece_Links(t *testing.T) {
	got := loadOnePiece(t)

	wantLinks := []metadata.Link{
		{Label: "MyAnimeList", URL: "https://myanimelist.net/manga/13"},
		{Label: "AniList", URL: "https://anilist.co/manga/30013"},
		{Label: "Anime-Planet", URL: "https://www.anime-planet.com/manga/one-piece"},
		{Label: "BookWalker", URL: "https://bookwalker.jp/series/13002/list"},
		{Label: "MangaUpdates", URL: "https://www.mangaupdates.com/series/pb8uwds"},
		{Label: "Kitsu", URL: "https://kitsu.io/manga/one-piece"},
		{Label: "Amazon", URL: "https://www.amazon.co.jp/dp/B07CKFRZGW"},
		{Label: "eBookJapan", URL: "https://ebookjapan.yahoo.co.jp/books/145222/"},
		{Label: "CDJapan", URL: "https://www.cdjapan.co.jp/product/NEOBK-2439974"},
		{Label: "Raw", URL: "https://shonenjumpplus.com/episode/10833519556325021794"},
		{Label: "Official English", URL: "https://mangaplus.shueisha.co.jp/titles/100020"},
	}
	if len(got.Links) != len(wantLinks) {
		t.Fatalf("Links = %+v, want %d entries", got.Links, len(wantLinks))
	}
	for i, want := range wantLinks {
		if got.Links[i] != want {
			t.Errorf("Links[%d] = %+v, want %+v", i, got.Links[i], want)
		}
	}
}

// TestToSeriesMetadata_OnePiece_AltTitleTypes asserts the ROMAJI/NATIVE/
// LOCALIZED/SYNONYM inference: the trimmed fixture keeps "ja"/"zh"(x3)/
// "ko"/"en" entries. The "en" one ("One Piece") must be typed LOCALIZED,
// the "ja" one (original language) NATIVE, everything else SYNONYM
// (MangaDex never tags a bare alt title "-ro" outside the primary title
// map).
func TestToSeriesMetadata_OnePiece_AltTitleTypes(t *testing.T) {
	got := loadOnePiece(t)

	foundEN, foundJA := false, false
	for _, at := range got.AltTitles {
		switch at.Lang {
		case "en":
			foundEN = true
			if at.Type != "LOCALIZED" {
				t.Errorf("AltTitle en Type = %q, want LOCALIZED", at.Type)
			}
		case "ja":
			foundJA = true
			if at.Type != "NATIVE" {
				t.Errorf("AltTitle ja Type = %q, want NATIVE", at.Type)
			}
		case "zh":
			if at.Type != "SYNONYM" {
				t.Errorf("AltTitle zh Type = %q, want SYNONYM", at.Type)
			}
		}
	}
	if !foundEN || !foundJA {
		t.Errorf("AltTitles missing expected en/ja entries: %+v", got.AltTitles)
	}
}

// TestToSearchResult_SoloLevelingRagnarok maps the first entry of the
// captured search-page fixture (GET /manga?title=solo+leveling&
// includes[]=cover_art) and asserts the SearchResult shape.
func TestToSearchResult_SoloLevelingRagnarok(t *testing.T) {
	var page mangaCollectionResponse
	loadFixture(t, "testdata/manga_search.json", &page)

	if len(page.Data) < 2 {
		t.Fatalf("fixture has %d entries, want >= 2", len(page.Data))
	}

	got := toSearchResult(page.Data[0])

	if got.Provider != Key {
		t.Errorf("Provider = %q, want %q", got.Provider, Key)
	}
	if got.RemoteID != "ade0306c-f4b6-4890-9edb-1ddf04df2039" {
		t.Errorf("RemoteID = %q, want the fixture's manga id", got.RemoteID)
	}
	// The fixture's title map carries only "ko-ro" — no "en" key — so
	// pickTitle must fall back to it, mirroring the One Piece case.
	if got.Title != "Na Honjaman Level Up: Ragnarok" {
		t.Errorf("Title = %q, want the ko-ro fallback title", got.Title)
	}
	if got.Year != 2024 {
		t.Errorf("Year = %d, want 2024", got.Year)
	}
	wantURL := "https://mangadex.org/title/ade0306c-f4b6-4890-9edb-1ddf04df2039"
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
	wantCover := "https://uploads.mangadex.org/covers/ade0306c-f4b6-4890-9edb-1ddf04df2039/fe76445d-387f-4ff6-8340-f06403c20dbe.jpg.512.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
}

// TestSplitTags_UnknownGroupNamesAreDropped proves a tag with no English
// name is skipped rather than surfacing an empty string in Genres/Tags.
func TestSplitTags_UnknownGroupNamesAreDropped(t *testing.T) {
	tags := []mangaTag{
		{Attributes: struct {
			Name  map[string]string `json:"name"`
			Group string            `json:"group"`
		}{Name: map[string]string{"ja": "アクション"}, Group: "genre"}}, // no "en" -> dropped
		{Attributes: struct {
			Name  map[string]string `json:"name"`
			Group string            `json:"group"`
		}{Name: map[string]string{"en": "Isekai"}, Group: "theme"}},
	}

	genres, others := splitTags(tags)

	if len(genres) != 0 {
		t.Errorf("Genres = %v, want empty (no English name)", genres)
	}
	if len(others) != 1 || others[0] != "Isekai" {
		t.Errorf("Tags = %v, want [Isekai]", others)
	}
}

// TestPickTitle_FallsBackDeterministically proves pickTitle prefers "en"
// when present, and otherwise picks the lexicographically smallest locale
// key so repeated calls over the same map are stable despite Go's
// randomized map iteration order.
func TestPickTitle_FallsBackDeterministically(t *testing.T) {
	if got := pickTitle(map[string]string{"ja-ro": "Wan Pisu", "en": "One Piece"}); got != "One Piece" {
		t.Errorf("pickTitle = %q, want en preferred", got)
	}
	// No "en": "ja-ro" < "zh" lexicographically, so "ja-ro" must win,
	// checked over many calls to catch any hidden map-order dependence.
	for i := 0; i < 20; i++ {
		if got := pickTitle(map[string]string{"zh": "Hai Zei Wang", "ja-ro": "Wan Pisu"}); got != "Wan Pisu" {
			t.Fatalf("pickTitle = %q, want deterministic ja-ro fallback", got)
		}
	}
	if got := pickTitle(map[string]string{}); got != "" {
		t.Errorf("pickTitle(empty) = %q, want \"\"", got)
	}
}

// TestAltTitleType_LocaleVocabulary pins the ROMAJI/NATIVE/LOCALIZED/
// SYNONYM inference rule (mapper.go altTitleType) against representative
// locale keys.
func TestAltTitleType_LocaleVocabulary(t *testing.T) {
	cases := []struct {
		locale, originalLanguage, want string
	}{
		{"ja-ro", "ja", "ROMAJI"},
		{"ja", "ja", "NATIVE"},
		{"en", "ja", "LOCALIZED"},
		{"fr", "ja", "SYNONYM"},
	}
	for _, tc := range cases {
		if got := altTitleType(tc.locale, tc.originalLanguage); got != tc.want {
			t.Errorf("altTitleType(%q,%q) = %q, want %q", tc.locale, tc.originalLanguage, got, tc.want)
		}
	}
}

// TestCoverURL_EmptyFileNameYieldsEmptyURL proves coverURL never builds a
// malformed URL from an absent cover.
func TestCoverURL_EmptyFileNameYieldsEmptyURL(t *testing.T) {
	if got := coverURL("some-id", ""); got != "" {
		t.Errorf("coverURL with empty fileName = %q, want \"\"", got)
	}
}

// TestCoversFixture_MapsGalleryEntries maps the captured cover-gallery
// fixture (GET /cover?manga[]=...) the same way covers.go's Covers method
// does, offline — proving the CoverURL/Label construction without a
// network call.
func TestCoversFixture_MapsGalleryEntries(t *testing.T) {
	var page coverCollectionResponse
	loadFixture(t, "testdata/covers.json", &page)

	if len(page.Data) == 0 {
		t.Fatal("fixture has no cover entries")
	}

	const mangaID = "a1c7c817-4e59-43b7-9365-09675a149a6f"
	first := page.Data[0]
	gotURL := coverURL(mangaID, first.Attributes.FileName)
	wantURL := "https://uploads.mangadex.org/covers/" + mangaID + "/" + first.Attributes.FileName + ".512.jpg"
	if gotURL != wantURL {
		t.Errorf("coverURL = %q, want %q", gotURL, wantURL)
	}

	gotLabel := coverLabel(first.Attributes.Volume, first.Attributes.Locale)
	wantLabel := "Vol. " + first.Attributes.Volume + " (" + first.Attributes.Locale + ")"
	if gotLabel != wantLabel {
		t.Errorf("coverLabel = %q, want %q", gotLabel, wantLabel)
	}
}

// TestCoverLabel_Fallbacks pins coverLabel's degraded shapes when volume
// or locale (or both) are absent.
func TestCoverLabel_Fallbacks(t *testing.T) {
	cases := []struct{ volume, locale, want string }{
		{"5", "en", "Vol. 5 (en)"},
		{"5", "", "Vol. 5"},
		{"", "en", "(en)"},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := coverLabel(tc.volume, tc.locale); got != tc.want {
			t.Errorf("coverLabel(%q,%q) = %q, want %q", tc.volume, tc.locale, got, tc.want)
		}
	}
}
