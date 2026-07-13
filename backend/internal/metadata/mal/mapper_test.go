// Package mal mapper tests run white-box (package mal, not mal_test)
// because toSeriesMetadata/toSearchResult/altTitles/etc. are unexported
// mapping internals with no public equivalent — there is no network-free
// way to exercise them through the exported Client without an injectable
// base URL this task's scope doesn't call for. Every fixture here was
// captured from a REAL api.myanimelist.net response (see shape_test.go,
// run 2026-07-13 against a live, approved MAL app) — never fabricated.
package mal

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

// loadSoloLeveling maps the captured Solo Leveling manga-detail fixture
// (GET /manga/121496?fields=...) once per subtest, so each field's
// assertions stay a small, independently readable (and independently
// cognitive-complexity-scored) test.
func loadSoloLeveling(t *testing.T) metadata.SeriesMetadata {
	t.Helper()
	var detail mangaDetail
	loadFixture(t, "testdata/manga_detail.json", &detail)
	return toSeriesMetadata(detail)
}

// TestToSeriesMetadata_SoloLeveling_Scalars asserts the plain scalar fields.
func TestToSeriesMetadata_SoloLeveling_Scalars(t *testing.T) {
	got := loadSoloLeveling(t)

	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want %q", got.Title, "Solo Leveling")
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q (MAL finished -> completed)", got.Status, "completed")
	}
	if got.Year != 2018 {
		t.Errorf("Year = %d, want 2018", got.Year)
	}
	if got.Description == "" {
		t.Error("Description is empty, want the fixture's synopsis text")
	}
	// mean 8.56 * 10 = 85.6 (0-10 scale -> 0-100).
	const wantScore = 85.6
	if diff := got.Score - wantScore; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("Score = %v, want ~%v (mean 8.56 x 10)", got.Score, wantScore)
	}
	wantCover := "https://cdn.myanimelist.net/images/manga/3/222295l.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
	// Publisher has no field in this provider's requested field list —
	// must stay unset (sensible default, per this package's contract).
	if got.Publisher != "" {
		t.Errorf("Publisher = %q, want empty (MAL has no publisher field requested)", got.Publisher)
	}
}

// TestToSeriesMetadata_SoloLeveling_Genres asserts the flattened genre-name
// list. Tags stays unset — MAL's manga fields carry no tag list, unlike
// AniList/MangaDex.
func TestToSeriesMetadata_SoloLeveling_Genres(t *testing.T) {
	got := loadSoloLeveling(t)

	wantGenres := []string{"Action", "Adult Cast", "Adventure", "Fantasy", "Urban Fantasy"}
	if !equalStrings(got.Genres, wantGenres) {
		t.Errorf("Genres = %v, want %v", got.Genres, wantGenres)
	}
	if got.Tags != nil {
		t.Errorf("Tags = %v, want nil (MAL requests no tags field)", got.Tags)
	}
}

// TestToSeriesMetadata_SoloLeveling_AltTitles asserts en+ja+synonyms map in
// LOCALIZED/NATIVE/SYNONYM order.
func TestToSeriesMetadata_SoloLeveling_AltTitles(t *testing.T) {
	got := loadSoloLeveling(t)

	wantAlt := []metadata.AltTitle{
		{Name: "Solo Leveling", Type: "LOCALIZED"},
		{Name: "나 혼자만 레벨업", Type: "NATIVE"},
		{Name: "Na Honjaman Level Up", Type: "SYNONYM"},
		{Name: "I Level Up Alone", Type: "SYNONYM"},
	}
	if len(got.AltTitles) != len(wantAlt) {
		t.Fatalf("len(AltTitles) = %d, want %d: %+v", len(got.AltTitles), len(wantAlt), got.AltTitles)
	}
	for i, w := range wantAlt {
		if got.AltTitles[i] != w {
			t.Errorf("AltTitles[%d] = %+v, want %+v", i, got.AltTitles[i], w)
		}
	}
}

// TestToSeriesMetadata_SoloLeveling_Authors asserts every credited author
// (including a last-name-only entry) maps with its raw role preserved.
func TestToSeriesMetadata_SoloLeveling_Authors(t *testing.T) {
	got := loadSoloLeveling(t)

	wantAuthors := []metadata.Author{
		{Name: "Chugong", Role: "Story"},
		{Name: "Sung-rak Jang", Role: "Art"},
		{Name: "Disciples", Role: "Art"},
	}
	if len(got.Authors) != len(wantAuthors) {
		t.Fatalf("len(Authors) = %d, want %d: %+v", len(got.Authors), len(wantAuthors), got.Authors)
	}
	for i, w := range wantAuthors {
		if got.Authors[i] != w {
			t.Errorf("Authors[%d] = %+v, want %+v", i, got.Authors[i], w)
		}
	}
}

// TestToSearchResult_RealFixture asserts metadata.SearchResult maps
// correctly from a real captured MAL search response for "Solo Leveling"
// (3 hits) — proves Provider/RemoteID/Title/URL/CoverURL/Year all map from
// the search entry point too (not just the single-lookup one).
func TestToSearchResult_RealFixture(t *testing.T) {
	var page mangaListResponse
	loadFixture(t, "testdata/manga_search.json", &page)

	if len(page.Data) != 3 {
		t.Fatalf("fixture has %d results, want 3", len(page.Data))
	}

	got := toSearchResult(page.Data[0].Node)
	want := metadata.SearchResult{
		Provider: "mal",
		RemoteID: "121496",
		Title:    "Solo Leveling",
		URL:      "https://myanimelist.net/manga/121496",
		CoverURL: "https://cdn.myanimelist.net/images/manga/3/222295l.jpg",
		Year:     2018,
	}
	if got != want {
		t.Errorf("toSearchResult(data[0].node) = %+v, want %+v", got, want)
	}

	// Third hit: a distinct id and title (a spin-off, not the base series).
	got2 := toSearchResult(page.Data[2].Node)
	if got2.RemoteID != "172429" {
		t.Errorf("third result RemoteID = %q, want %q", got2.RemoteID, "172429")
	}
	if got2.Title != "Solo Leveling: Ragnarok" {
		t.Errorf("third result Title = %q, unexpected", got2.Title)
	}
}

// --- Synthetic edge-case tests -------------------------------------------
//
// These exercise mapping branches the captured live fixtures don't hit (MAL
// didn't happen to return a manga with every status enum value, a blank
// start_date, or an author with only a first name). The field-name/shape
// correctness is already proven against the real fixtures above; these are
// pure-function branch tests over that same (already-shape-verified)
// mangaDetail/mangaNode structs.

func TestNormalizeStatus_AllKnownValues(t *testing.T) {
	cases := map[string]string{
		"currently_publishing": "ongoing",
		"finished":             "completed",
		"on_hiatus":            "hiatus",
		"not_yet_published":    "",
		"discontinued":         "",
		"":                     "",
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestYearFromStartDate(t *testing.T) {
	cases := map[string]int{
		"2018-03-04": 2018,
		"2018-03":    2018,
		"2018":       2018,
		"":           0,
		"abc":        0,
		"20":         0,
	}
	for in, want := range cases {
		if got := yearFromStartDate(in); got != want {
			t.Errorf("yearFromStartDate(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestAuthorName_HandlesMissingHalves(t *testing.T) {
	cases := []struct {
		name string
		node authorNode
		want string
	}{
		{"both names", authorNode{FirstName: "Sung-rak", LastName: "Jang"}, "Sung-rak Jang"},
		{"last name only", authorNode{FirstName: "", LastName: "Disciples"}, "Disciples"},
		{"first name only", authorNode{FirstName: "Prodigy", LastName: ""}, "Prodigy"},
		{"both blank", authorNode{}, ""},
	}
	for _, c := range cases {
		if got := authorName(c.node); got != c.want {
			t.Errorf("%s: authorName(%+v) = %q, want %q", c.name, c.node, got, c.want)
		}
	}
}

func TestAuthors_SkipsEntryWithNoUsableName(t *testing.T) {
	list := []mangaAuthor{
		{Node: authorNode{LastName: "Real Name"}, Role: "Art"},
		{Node: authorNode{}, Role: "Ghost"},
	}
	got := authors(list)
	if len(got) != 1 || got[0].Name != "Real Name" {
		t.Errorf("authors(list) = %+v, want exactly the one entry with a non-blank name", got)
	}
}

func TestToSeriesMetadata_EmptyCollectionsMapToNil(t *testing.T) {
	d := mangaDetail{ID: 1, Title: "Empty"}
	got := toSeriesMetadata(d)

	if got.Genres != nil {
		t.Errorf("Genres = %v, want nil for a record with no genres", got.Genres)
	}
	if got.Authors != nil {
		t.Errorf("Authors = %v, want nil for a record with no authors", got.Authors)
	}
	if got.AltTitles != nil {
		t.Errorf("AltTitles = %v, want nil when en/ja/synonyms are all blank", got.AltTitles)
	}
	if got.Tags != nil {
		t.Errorf("Tags = %v, want nil (never populated)", got.Tags)
	}
	if got.Links != nil {
		t.Errorf("Links = %v, want nil (never populated)", got.Links)
	}
}

func TestTruncateQuery(t *testing.T) {
	short := "One Piece"
	if got := truncateQuery(short); got != short {
		t.Errorf("truncateQuery(%q) = %q, want unchanged", short, got)
	}

	long := make([]rune, 100)
	for i := range long {
		long[i] = 'a'
	}
	got := truncateQuery(string(long))
	gotRunes := []rune(got)
	if len(gotRunes) != maxQueryLen {
		t.Errorf("truncateQuery(100 chars) length = %d, want %d", len(gotRunes), maxQueryLen)
	}

	// Rune-safety: a query padded with multi-byte runes past the limit must
	// truncate on a rune boundary, never split a multi-byte character.
	multiByte := ""
	for i := 0; i < 70; i++ {
		multiByte += "한"
	}
	gotMB := truncateQuery(multiByte)
	if n := len([]rune(gotMB)); n != maxQueryLen {
		t.Errorf("truncateQuery(70 multi-byte runes) rune length = %d, want %d", n, maxQueryLen)
	}
}

func TestExtFromURL(t *testing.T) {
	cases := map[string]string{
		"https://cdn.myanimelist.net/images/manga/3/222295l.jpg": "jpg",
		"https://cdn.myanimelist.net/images/manga/3/222295l.png": "png",
		"https://cdn.myanimelist.net/images/manga/3/noext":       "jpg",
	}
	for in, want := range cases {
		if got := extFromURL(in); got != want {
			t.Errorf("extFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// equalStrings compares two string slices for exact ordered equality.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
