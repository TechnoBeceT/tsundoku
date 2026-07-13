package anilist

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// loadFixture decodes a captured real AniList response (see testdata/) into
// out. These files are REAL responses (curl'd against graphql.anilist.co,
// see the A2 task report), not fabricated JSON — DISCOVERY-FIRST per
// plan/metadata-engine-phase1.
func loadFixture(t *testing.T, name string, out any) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name) //nolint:gosec // test-only; name is always a literal passed by a test in this file, never external input
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var envelope gqlResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal fixture %s envelope: %v", name, err)
	}
	if len(envelope.Errors) > 0 {
		t.Fatalf("fixture %s carries GraphQL errors: %+v", name, envelope.Errors)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatalf("unmarshal fixture %s data: %v", name, err)
	}
}

// TestToSeriesMetadata_RealFixture asserts every metadata.SeriesMetadata
// field maps correctly from a real captured AniList Media(id: 105398)
// response ("Solo Leveling") — chosen because it exercises every field this
// mapper touches: an English title distinct from the romaji/native ones,
// a full synonym list, tags, multiple staff roles (including a
// non-ASCII-punctuation role string), a start year, a non-zero score, a
// cover URL, and a long externalLinks list.
// TestToSeriesMetadata_RealFixture asserts every metadata.SeriesMetadata
// field maps correctly from a real captured AniList Media(id: 105398)
// response ("Solo Leveling") — chosen because it exercises every field this
// mapper touches: an English title distinct from the romaji/native ones,
// a full synonym list, tags, multiple staff roles (including a
// non-ASCII-punctuation role string), a start year, a non-zero score, a
// cover URL, and a long externalLinks list. Split into subtests (each its
// own assertion group) to keep per-function cyclomatic complexity low.
func TestToSeriesMetadata_RealFixture(t *testing.T) {
	var data mediaData
	loadFixture(t, "media_by_id_solo_leveling.json", &data)
	got := toSeriesMetadata(data.Media)

	t.Run("identity", func(t *testing.T) { assertIdentityFields(t, got) })
	t.Run("collections", func(t *testing.T) { assertScalarCollectionFields(t, got) })
	t.Run("altTitles", func(t *testing.T) { assertAltTitles(t, got) })
	t.Run("authors", func(t *testing.T) { assertAuthors(t, got) })
	t.Run("links", func(t *testing.T) { assertLinks(t, got) })
}

// assertIdentityFields checks Title/Description/Status/Year/Score/CoverURL/
// Publisher — the single-valued fields that identify and rate the series.
func assertIdentityFields(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want %q (english should win over romaji)", got.Title, "Solo Leveling")
	}
	if got.Description == "" {
		t.Error("Description is empty, want the fixture's synopsis text")
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q (AniList FINISHED -> completed)", got.Status, "completed")
	}
	if got.Year != 2018 {
		t.Errorf("Year = %d, want 2018", got.Year)
	}
	if got.Score != 84 {
		t.Errorf("Score = %v, want 84", got.Score)
	}
	wantCover := "https://s4.anilist.co/file/anilistcdn/media/manga/cover/large/bx105398-b673Vt5ZSuz3.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
	// Publisher has no AniList field in this selection — must stay unset.
	if got.Publisher != "" {
		t.Errorf("Publisher = %q, want empty (AniList has no publisher field selected)", got.Publisher)
	}
}

// assertScalarCollectionFields checks Genres and Tags — the two plain
// string-slice fields toSeriesMetadata maps straight from AniList.
func assertScalarCollectionFields(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	wantGenres := []string{"Action", "Adventure", "Fantasy"}
	if !equalStrings(got.Genres, wantGenres) {
		t.Errorf("Genres = %v, want %v", got.Genres, wantGenres)
	}
	if len(got.Tags) != 31 {
		t.Errorf("len(Tags) = %d, want 31 (fixture tag count)", len(got.Tags))
	}
	if len(got.Tags) > 0 && got.Tags[0] != "Dungeon" {
		t.Errorf("Tags[0] = %q, want %q", got.Tags[0], "Dungeon")
	}
}

// assertAltTitles checks romaji + english + native + every synonym, each
// tagged with the right AltTitle.Type, in order.
func assertAltTitles(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	wantAlt := []metadata.AltTitle{
		{Name: "Na Honjaman Level Up", Type: "ROMAJI"},
		{Name: "Solo Leveling", Type: "LOCALIZED"},
		{Name: "나 혼자만 레벨업", Type: "NATIVE"},
		{Name: "I Level Up Alone", Type: "SYNONYM"},
		{Name: "Only I Level Up", Type: "SYNONYM"},
		{Name: "俺だけレベルアップな件", Type: "SYNONYM"},
		{Name: "我獨自升級", Type: "SYNONYM"},
		{Name: "Ore dake Level Up na Ken", Type: "SYNONYM"},
		{Name: "Поднятие уровня в одиночку", Type: "SYNONYM"},
		{Name: "Тільки я візьму новий рівень", Type: "SYNONYM"},
		{Name: "Вебтун. Поднятие уровня в одиночку. Solo Leveling", Type: "SYNONYM"},
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

// assertAuthors checks every staff edge's raw role string is preserved
// verbatim (never normalized into a closed enum).
func assertAuthors(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	wantAuthors := []metadata.Author{
		{Name: "Seong-Rak Jang", Role: "Art"},
		{Name: "So-Ryeong Gi", Role: "Story (chs 1-92)"},
		{Name: "Chu-Gong", Role: "Original Story"},
		{Name: "Hyeon-Gun", Role: "Story (chs 93-201)"},
		{Name: "Abigail Blackman", Role: "Lettering (English)"},
		{Name: "Adrianna Jarzębowska", Role: "Translator (Polish)"},
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

// assertLinks checks every externalLinks entry maps site->Label, url->URL,
// preserving order.
func assertLinks(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	if len(got.Links) != 13 {
		t.Fatalf("len(Links) = %d, want 13 (fixture externalLinks count)", len(got.Links))
	}
	wantFirstLink := metadata.Link{Label: "KakaoPage", URL: "https://page.kakao.com/content/50866481"}
	if got.Links[0] != wantFirstLink {
		t.Errorf("Links[0] = %+v, want %+v", got.Links[0], wantFirstLink)
	}
}

// TestToSearchResult_RealFixture asserts metadata.SearchResult maps
// correctly from a real captured AniList Page.media search response for
// "Solo Leveling" (2 hits) — proves the Provider field, RemoteID
// stringification, title preference, and URL/CoverURL/Year all map from
// the search entry point too (not just the single-lookup one).
func TestToSearchResult_RealFixture(t *testing.T) {
	var data searchPageData
	loadFixture(t, "search_solo_leveling.json", &data)

	if len(data.Page.Media) != 2 {
		t.Fatalf("fixture has %d results, want 2", len(data.Page.Media))
	}

	got := toSearchResult(data.Page.Media[0])
	want := metadata.SearchResult{
		Provider: "anilist",
		RemoteID: "105398",
		Title:    "Solo Leveling",
		URL:      "https://anilist.co/manga/105398",
		CoverURL: "https://s4.anilist.co/file/anilistcdn/media/manga/cover/large/bx105398-b673Vt5ZSuz3.jpg",
		Year:     2018,
	}
	if got != want {
		t.Errorf("toSearchResult(media[0]) = %+v, want %+v", got, want)
	}

	// Second hit: a different english title, distinct id.
	got2 := toSearchResult(data.Page.Media[1])
	if got2.RemoteID != "201652" {
		t.Errorf("second result RemoteID = %q, want %q", got2.RemoteID, "201652")
	}
	if got2.Title != "The Privilege of the Second Life is Power Leveling" {
		t.Errorf("second result Title = %q, unexpected", got2.Title)
	}
}

// --- Synthetic edge-case tests -------------------------------------------
//
// These exercise mapping branches the captured live fixtures don't hit
// (AniList didn't happen to return a manga with a blank English title,
// every AniList status enum value, or empty collections in one record).
// The field-name/shape correctness is already proven against the real
// fixtures above; these are pure-function branch tests over that same
// (already-shape-verified) gqlMedia struct.

func TestPreferredTitle_FallsBackToRomajiWhenEnglishBlank(t *testing.T) {
	got := preferredTitle(gqlTitle{Romaji: "Ore dake Level Up na Ken", English: "", Native: "俺だけレベルアップな件"})
	if got != "Ore dake Level Up na Ken" {
		t.Errorf("preferredTitle = %q, want romaji fallback", got)
	}
}

func TestNormalizeStatus_AllKnownValues(t *testing.T) {
	cases := map[string]string{
		"RELEASING":         "ongoing",
		"FINISHED":          "completed",
		"HIATUS":            "hiatus",
		"CANCELLED":         "cancelled",
		"NOT_YET_RELEASED":  "",
		"SOMETHING_UNKNOWN": "",
		"":                  "",
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToSeriesMetadata_EmptyCollectionsMapToNil(t *testing.T) {
	m := gqlMedia{ID: 1}
	got := toSeriesMetadata(m)

	if got.Tags != nil {
		t.Errorf("Tags = %v, want nil for a record with no tags", got.Tags)
	}
	if got.Authors != nil {
		t.Errorf("Authors = %v, want nil for a record with no staff", got.Authors)
	}
	if got.Links != nil {
		t.Errorf("Links = %v, want nil for a record with no externalLinks", got.Links)
	}
	if got.AltTitles != nil {
		t.Errorf("AltTitles = %v, want nil when every title slot + synonyms are blank", got.AltTitles)
	}
	if got.Genres != nil {
		t.Errorf("Genres = %v, want nil for a record with no genres", got.Genres)
	}
}

func TestAuthors_SkipsEdgeWithNoStaffName(t *testing.T) {
	staff := gqlStaffConnection{Edges: []gqlStaffEdge{
		{Role: "Art", Node: gqlStaffNode{Name: gqlStaffName{Full: "Real Name"}}},
		{Role: "Ghost", Node: gqlStaffNode{Name: gqlStaffName{Full: ""}}},
	}}
	got := authors(staff)
	if len(got) != 1 || got[0].Name != "Real Name" {
		t.Errorf("authors(staff) = %+v, want exactly the one edge with a non-blank name", got)
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
