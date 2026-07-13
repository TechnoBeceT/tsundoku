// Package kitsu mapper tests run white-box (package kitsu, not kitsu_test)
// because toSeriesMetadata/toSearchResult/buildAltTitles/etc. are
// unexported mapping internals with no public equivalent — there is no
// network-free way to exercise them through the exported Client without an
// injectable base URL this task's scope doesn't call for. Every fixture
// here was captured from a REAL kitsu.io/api/edge response (see
// shape_test.go) and trimmed only in array LENGTH (fewer abbreviated
// titles, fewer search hits) — never in field shape.
package kitsu

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

// loadOnePiece maps the captured One Piece manga-detail-with-categories
// fixture (GET /manga/38?include=categories) once per subtest — a status
// "current" (-> ongoing) series whose titles map carries all three of the
// explicitly-typed locales (en/en_jp/ja_jp) plus one extra ("en_us").
func loadOnePiece(t *testing.T) metadata.SeriesMetadata {
	t.Helper()
	var entity mangaEntityResponse
	loadFixture(t, "testdata/manga_detail_one_piece.json", &entity)
	return toSeriesMetadata(entity)
}

// loadSoloLeveling maps the captured Solo Leveling manga-detail fixture —
// a status "finished" (-> completed) series whose titles map carries NO
// "en" entry at all (only en_jp/en_kr/en_us/ja_jp/ko_kr), proving the
// mapper never assumes "en" is present.
func loadSoloLeveling(t *testing.T) metadata.SeriesMetadata {
	t.Helper()
	var entity mangaEntityResponse
	loadFixture(t, "testdata/manga_detail_solo_leveling.json", &entity)
	return toSeriesMetadata(entity)
}

// TestToSeriesMetadata_OnePiece_Scalars asserts the plain scalar fields.
func TestToSeriesMetadata_OnePiece_Scalars(t *testing.T) {
	got := loadOnePiece(t)

	if got.Title != "One Piece" {
		t.Errorf("Title = %q, want %q", got.Title, "One Piece")
	}
	if got.Status != "ongoing" {
		t.Errorf("Status = %q, want ongoing (Kitsu status=current)", got.Status)
	}
	if got.Year != 1997 {
		t.Errorf("Year = %d, want 1997", got.Year)
	}
	if got.Description == "" {
		t.Error("Description is empty, want the synopsis")
	}
	if got.Score != 85.07 {
		t.Errorf("Score = %v, want 85.07 (parsed from averageRating string)", got.Score)
	}
	wantCover := "https://media.kitsu.app/manga/38/poster_image/85c98b3ebfea8a1cfe8c8f837b6b5fc8.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
}

// TestToSeriesMetadata_OnePiece_Links asserts the single self-referential
// Kitsu page link is built from the manga's slug.
func TestToSeriesMetadata_OnePiece_Links(t *testing.T) {
	got := loadOnePiece(t)

	wantLinks := []metadata.Link{{Label: "Kitsu", URL: "https://kitsu.io/manga/one-piece"}}
	if len(got.Links) != 1 || got.Links[0] != wantLinks[0] {
		t.Errorf("Links = %+v, want %+v", got.Links, wantLinks)
	}
}

// TestToSeriesMetadata_OnePiece_Genres asserts Genres is resolved from the
// categories relationship joined against the top-level `included` array,
// IN THE RELATIONSHIP'S OWN ORDER — the load-bearing join this provider was
// built fresh to prove (Kitsu has no Komf reference for it).
func TestToSeriesMetadata_OnePiece_Genres(t *testing.T) {
	got := loadOnePiece(t)

	want := []string{"Comedy", "Super Power", "Fantasy", "Action", "Friendship", "Adventure", "Shounen", "Pirate", "Drama"}
	if len(got.Genres) != len(want) {
		t.Fatalf("Genres = %v, want %v", got.Genres, want)
	}
	for i, g := range want {
		if got.Genres[i] != g {
			t.Errorf("Genres[%d] = %q, want %q", i, got.Genres[i], g)
		}
	}
}

// TestToSeriesMetadata_OnePiece_AltTitles asserts the full ordered
// AltTitles build: the three explicitly-typed locales first (en ->
// LOCALIZED, en_jp -> ROMAJI, ja_jp -> NATIVE), then the one extra locale
// key ("en_us", sorted-deterministic) as SYNONYM, then every trimmed
// abbreviatedTitles entry (5, per the fixture) as SYNONYM with no Lang.
func TestToSeriesMetadata_OnePiece_AltTitles(t *testing.T) {
	got := loadOnePiece(t)

	want := []metadata.AltTitle{
		{Name: "One Piece", Type: "LOCALIZED", Lang: "en"},
		{Name: "One Piece", Type: "ROMAJI", Lang: "en_jp"},
		{Name: "ONE PIECE", Type: "NATIVE", Lang: "ja_jp"},
		{Name: "One Piece", Type: "SYNONYM", Lang: "en_us"},
		{Name: "Budak Getah", Type: "SYNONYM"},
		{Name: "Đảo Hải Tặc", Type: "SYNONYM"},
		{Name: "Большой куш", Type: "SYNONYM"},
		{Name: "Ван Пис", Type: "SYNONYM"},
		{Name: "Уан Пийс", Type: "SYNONYM"},
	}
	if len(got.AltTitles) != len(want) {
		t.Fatalf("AltTitles = %+v, want %d entries: %+v", got.AltTitles, len(want), want)
	}
	for i, w := range want {
		if got.AltTitles[i] != w {
			t.Errorf("AltTitles[%d] = %+v, want %+v", i, got.AltTitles[i], w)
		}
	}
}

// TestToSeriesMetadata_SoloLeveling_NoENTitle proves the mapper never
// assumes an "en" title key is present: Solo Leveling's titles map has none
// at all, so LOCALIZED must simply be absent from AltTitles rather than the
// mapper panicking or emitting a spurious empty entry.
func TestToSeriesMetadata_SoloLeveling_NoENTitle(t *testing.T) {
	got := loadSoloLeveling(t)

	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want %q", got.Title, "Solo Leveling")
	}
	for _, at := range got.AltTitles {
		if at.Type == "LOCALIZED" {
			t.Errorf("AltTitles contains a LOCALIZED entry %+v, want none (fixture has no \"en\" title key)", at)
		}
	}
}

// TestToSeriesMetadata_SoloLeveling_StatusFinishedMapsCompleted asserts the
// "finished" -> "completed" leg of mapStatus against a real fixture (the
// One Piece fixture only exercises "current" -> "ongoing").
func TestToSeriesMetadata_SoloLeveling_StatusFinishedMapsCompleted(t *testing.T) {
	got := loadSoloLeveling(t)

	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed (Kitsu status=finished)", got.Status)
	}
}

// TestToSeriesMetadata_SoloLeveling_Genres asserts the categories join
// against a second, independently-captured fixture (4 categories, smaller
// than One Piece's 9) so the join logic is proven on more than one shape.
func TestToSeriesMetadata_SoloLeveling_Genres(t *testing.T) {
	got := loadSoloLeveling(t)

	want := []string{"Adventure", "Action", "Fantasy", "Shounen"}
	if len(got.Genres) != len(want) {
		t.Fatalf("Genres = %v, want %v", got.Genres, want)
	}
	for i, g := range want {
		if got.Genres[i] != g {
			t.Errorf("Genres[%d] = %q, want %q", i, got.Genres[i], g)
		}
	}
}

// TestToSeriesMetadata_SoloLeveling_ROMAJIAndNATIVE spot-checks the ROMAJI/
// NATIVE typing survives even without an "en" entry, and that the "others"
// bucket (en_kr/en_us/ko_kr, none of them one of the three labeled locales)
// all land as SYNONYM in sorted-locale order.
func TestToSeriesMetadata_SoloLeveling_ROMAJIAndNATIVE(t *testing.T) {
	got := loadSoloLeveling(t)

	if len(got.AltTitles) < 2 {
		t.Fatalf("AltTitles = %+v, want at least the ROMAJI+NATIVE pair", got.AltTitles)
	}
	if got.AltTitles[0] != (metadata.AltTitle{Name: "Boku dake Level Up na Ken", Type: "ROMAJI", Lang: "en_jp"}) {
		t.Errorf("AltTitles[0] = %+v, want the en_jp ROMAJI entry", got.AltTitles[0])
	}
	if got.AltTitles[1] != (metadata.AltTitle{Name: "俺だけレベルアップな件", Type: "NATIVE", Lang: "ja_jp"}) {
		t.Errorf("AltTitles[1] = %+v, want the ja_jp NATIVE entry", got.AltTitles[1])
	}

	wantOthers := []metadata.AltTitle{
		{Name: "Na Honjaman Level Up", Type: "SYNONYM", Lang: "en_kr"},
		{Name: "Solo Leveling", Type: "SYNONYM", Lang: "en_us"},
		{Name: "나 혼자만 레벨업", Type: "SYNONYM", Lang: "ko_kr"},
	}
	if len(got.AltTitles) < 5 {
		t.Fatalf("AltTitles = %+v, want at least 5 entries before abbreviatedTitles", got.AltTitles)
	}
	for i, w := range wantOthers {
		if got.AltTitles[2+i] != w {
			t.Errorf("AltTitles[%d] = %+v, want %+v", 2+i, got.AltTitles[2+i], w)
		}
	}
}

// TestToSearchResult_OnePiece maps the first entry of the captured search
// fixture (GET /manga?filter[text]=one+piece) and asserts the
// metadata.SearchResult shape.
func TestToSearchResult_OnePiece(t *testing.T) {
	var page mangaCollectionResponse
	loadFixture(t, "testdata/search_one_piece.json", &page)

	if len(page.Data) < 2 {
		t.Fatalf("fixture has %d entries, want >= 2", len(page.Data))
	}

	got := toSearchResult(page.Data[0])

	if got.Provider != Key {
		t.Errorf("Provider = %q, want %q", got.Provider, Key)
	}
	if got.RemoteID != "38" {
		t.Errorf("RemoteID = %q, want the fixture's manga id", got.RemoteID)
	}
	if got.Title != "One Piece" {
		t.Errorf("Title = %q, want %q", got.Title, "One Piece")
	}
	if got.URL != "https://kitsu.io/manga/one-piece" {
		t.Errorf("URL = %q, want the slug-derived Kitsu page URL", got.URL)
	}
	if got.Year != 1997 {
		t.Errorf("Year = %d, want 1997", got.Year)
	}
	wantCover := "https://media.kitsu.app/manga/38/poster_image/85c98b3ebfea8a1cfe8c8f837b6b5fc8.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
}

// TestToSearchResult_SecondEntryIsDistinctManga proves toSearchResult maps
// each entry independently (not accidentally reusing the first hit's data)
// by checking the fixture's second, different manga.
func TestToSearchResult_SecondEntryIsDistinctManga(t *testing.T) {
	var page mangaCollectionResponse
	loadFixture(t, "testdata/search_one_piece.json", &page)

	got := toSearchResult(page.Data[1])
	if got.RemoteID != "4194" {
		t.Errorf("RemoteID = %q, want 4194 (One Piece: Strong World)", got.RemoteID)
	}
	if got.Title != "One Piece: Strong World" {
		t.Errorf("Title = %q, want %q", got.Title, "One Piece: Strong World")
	}
}

// TestParseYear pins the "YYYY-MM-DD" leading-segment parse plus its
// empty/malformed fallbacks.
func TestParseYear(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1997-07-22", 1997},
		{"2018-03-04", 2018},
		{"", 0},
		{"not-a-date", 0},
	}
	for _, tc := range cases {
		if got := parseYear(tc.in); got != tc.want {
			t.Errorf("parseYear(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestParseScore pins the string-percentage parse plus its empty/malformed
// fallbacks.
func TestParseScore(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"85.07", 85.07},
		{"0", 0},
		{"", 0},
		{"not-a-number", 0},
	}
	for _, tc := range cases {
		if got := parseScore(tc.in); got != tc.want {
			t.Errorf("parseScore(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestMapStatus pins the observed-live current/finished mapping plus the
// defensive (never-observed) hiatus/cancelled/discontinued handling and the
// tba/unreleased/upcoming/unknown -> "" fallback.
func TestMapStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"current", "ongoing"},
		{"finished", "completed"},
		{"hiatus", "hiatus"},
		{"cancelled", "cancelled"},
		{"discontinued", "cancelled"},
		{"tba", ""},
		{"unreleased", ""},
		{"upcoming", ""},
		{"", ""},
		{"something-unrecognized", ""},
	}
	for _, tc := range cases {
		if got := mapStatus(tc.in); got != tc.want {
			t.Errorf("mapStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestResolveGenres_SkipsUnmatchedAndNonCategoryRefs proves a ref with no
// matching included record (never returned, or a different `included`
// type) is dropped rather than surfacing an empty-string genre.
func TestResolveGenres_SkipsUnmatchedAndNonCategoryRefs(t *testing.T) {
	refs := []resourceRef{{ID: "1", Type: "categories"}, {ID: "2", Type: "categories"}, {ID: "3", Type: "categories"}}
	included := []includedRecord{
		{ID: "1", Type: "categories", Attributes: includedAttributes{Title: "Action"}},
		{ID: "2", Type: "genres", Attributes: includedAttributes{Title: "Should Not Match"}}, // wrong type
		// id "3" absent from included entirely (e.g. not returned by the API).
	}

	got := resolveGenres(refs, included)
	want := []string{"Action"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("resolveGenres = %v, want %v", got, want)
	}
}

// TestResolveGenres_EmptyRefsYieldsNilNotFetch proves an empty categories
// relationship (a manga with none) short-circuits to nil without touching
// included at all.
func TestResolveGenres_EmptyRefsYieldsNilNotFetch(t *testing.T) {
	if got := resolveGenres(nil, []includedRecord{{ID: "1", Type: "categories", Attributes: includedAttributes{Title: "Action"}}}); got != nil {
		t.Errorf("resolveGenres(nil refs) = %v, want nil", got)
	}
}

// TestKitsuSeriesURL pins the slug->URL build plus its empty-slug fallback.
func TestKitsuSeriesURL(t *testing.T) {
	if got := kitsuSeriesURL("one-piece"); got != "https://kitsu.io/manga/one-piece" {
		t.Errorf("kitsuSeriesURL(one-piece) = %q, want the Kitsu page URL", got)
	}
	if got := kitsuSeriesURL(""); got != "" {
		t.Errorf("kitsuSeriesURL(\"\") = %q, want \"\"", got)
	}
}

// TestBuildAltTitles_BlankNamesSkipped proves a blank title-map value or a
// blank abbreviatedTitles entry is skipped rather than emitting an
// AltTitle with an empty Name.
func TestBuildAltTitles_BlankNamesSkipped(t *testing.T) {
	got := buildAltTitles(map[string]string{"en": "", "ja_jp": "ONE PIECE"}, []string{"", "Real Abbrev"})
	want := []metadata.AltTitle{
		{Name: "ONE PIECE", Type: "NATIVE", Lang: "ja_jp"},
		{Name: "Real Abbrev", Type: "SYNONYM"},
	}
	if len(got) != len(want) {
		t.Fatalf("buildAltTitles = %+v, want %+v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("buildAltTitles[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestExtFromURL pins the bare-extension extraction plus its dotless-path
// "jpg" fallback (shared by GetSeriesCover — exercised here without a
// network call).
func TestExtFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://media.kitsu.app/manga/38/poster_image/abc.jpg", "jpg"},
		{"https://media.kitsu.app/manga/poster_images/54114/original.jpg", "jpg"},
		{"https://media.kitsu.app/manga/54114/cover_image/x.png", "png"},
		{"https://media.kitsu.app/manga/no-extension-path", "jpg"},
	}
	for _, tc := range cases {
		if got := extFromURL(tc.in); got != tc.want {
			t.Errorf("extFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
