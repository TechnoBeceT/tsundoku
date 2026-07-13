package mangaupdates

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// loadFixture decodes a captured real MangaUpdates response (see testdata/)
// into out. These files are REAL responses (curl'd against
// api.mangaupdates.com/v1, see the A4 task report), not fabricated JSON —
// DISCOVERY-FIRST per plan/metadata-engine-phase1. The search fixture is a
// real response TRIMMED to its first 3 results (a genuine subset, not
// invented data) to keep the file a reasonable size.
func loadFixture(t *testing.T, name string, out any) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name) //nolint:gosec // test-only; name is always a literal passed by a test in this file, never external input
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
}

// TestToSeriesMetadata_RealFixture asserts every metadata.SeriesMetadata
// field maps correctly from a real captured MangaUpdates GET /series/{id}
// response ("Solo Leveling", series_id 15180124327) — chosen because it
// exercises every field this mapper touches: a completed status, a long
// associated-title list, mixed Author/Artist credits, a genuine
// bayesian_rating, a large category (tag) list, and a populated cover.
func TestToSeriesMetadata_RealFixture(t *testing.T) {
	var detail seriesDetail
	loadFixture(t, "series_solo_leveling.json", &detail)
	got := toSeriesMetadata(detail)

	t.Run("identity", func(t *testing.T) { assertIdentityFields(t, got) })
	t.Run("collections", func(t *testing.T) { assertCollectionFields(t, got) })
	t.Run("altTitles", func(t *testing.T) { assertAltTitles(t, got) })
	t.Run("authors", func(t *testing.T) { assertAuthors(t, got) })
	t.Run("links", func(t *testing.T) { assertLinks(t, got) })
}

// assertIdentityFields checks Title/Description/Status/Year/Score/CoverURL
// — the single-valued fields that identify and rate the series.
func assertIdentityFields(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want %q", got.Title, "Solo Leveling")
	}
	wantDescPrefix := "From Yen Press:"
	if len(got.Description) < len(wantDescPrefix) || got.Description[:len(wantDescPrefix)] != wantDescPrefix {
		t.Errorf("Description = %q, want it to start with %q", got.Description, wantDescPrefix)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q (completed=true -> completed)", got.Status, "completed")
	}
	if got.Year != 2018 {
		t.Errorf("Year = %d, want 2018", got.Year)
	}
	if got.Score != 84.7 {
		t.Errorf("Score = %v, want 84.7 (bayesian_rating 8.47 * 10)", got.Score)
	}
	wantCover := "https://cdn.mangaupdates.com/image/i515366.jpg"
	if got.CoverURL != wantCover {
		t.Errorf("CoverURL = %q, want %q", got.CoverURL, wantCover)
	}
	// Publisher has no field this mapper reads (MangaUpdates' publishers
	// list is out of this task's mapped-field set) — must stay unset.
	if got.Publisher != "" {
		t.Errorf("Publisher = %q, want empty", got.Publisher)
	}
}

// assertCollectionFields checks Genres and Tags — the two plain
// string-slice fields toSeriesMetadata maps from genres[]/categories[].
func assertCollectionFields(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	wantGenres := []string{"Action", "Adventure", "Drama", "Fantasy", "Shounen"}
	if !equalStrings(got.Genres, wantGenres) {
		t.Errorf("Genres = %v, want %v", got.Genres, wantGenres)
	}
	if len(got.Tags) != 88 {
		t.Errorf("len(Tags) = %d, want 88 (fixture category count)", len(got.Tags))
	}
	if len(got.Tags) > 0 && got.Tags[0] != "21st Century" {
		t.Errorf("Tags[0] = %q, want %q", got.Tags[0], "21st Century")
	}
	if n := len(got.Tags); n > 0 && got.Tags[n-1] != "Younger Sister" {
		t.Errorf("Tags[last] = %q, want %q", got.Tags[n-1], "Younger Sister")
	}
}

// assertAltTitles checks every associated title maps to an AltTitle tagged
// SYNONYM, in order.
func assertAltTitles(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	if len(got.AltTitles) != 33 {
		t.Fatalf("len(AltTitles) = %d, want 33 (fixture associated count)", len(got.AltTitles))
	}
	wantFirst := metadata.AltTitle{Name: "Ben yanlız gelişirim", Type: "SYNONYM"}
	if got.AltTitles[0] != wantFirst {
		t.Errorf("AltTitles[0] = %+v, want %+v", got.AltTitles[0], wantFirst)
	}
	wantLast := metadata.AltTitle{Name: "나혼렙", Type: "SYNONYM"}
	if got.AltTitles[32] != wantLast {
		t.Errorf("AltTitles[32] = %+v, want %+v", got.AltTitles[32], wantLast)
	}
}

// assertAuthors checks every author entry's raw type string is preserved
// verbatim as Role (never normalized into a closed enum), in order.
func assertAuthors(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	wantAuthors := []metadata.Author{
		{Name: "Chugong", Role: "Author"},
		{Name: "H-goon", Role: "Author"},
		{Name: "KI Soryeong", Role: "Author"},
		{Name: "DISCIPLES (Redice Studio)", Role: "Artist"},
		{Name: "DUBU (Redice Studio)", Role: "Artist"},
		{Name: "Redice Studio", Role: "Artist"},
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

// assertLinks checks the single MangaUpdates self-link built from the
// series' url field.
func assertLinks(t *testing.T, got metadata.SeriesMetadata) {
	t.Helper()

	want := []metadata.Link{{Label: "MangaUpdates", URL: "https://www.mangaupdates.com/series/6z1uqw7/solo-leveling"}}
	if len(got.Links) != 1 || got.Links[0] != want[0] {
		t.Errorf("Links = %+v, want %+v", got.Links, want)
	}
}

// TestToSearchResult_RealFixture asserts metadata.SearchResult maps
// correctly from a real captured MangaUpdates POST /series/search response
// for "Solo Leveling" (trimmed to 3 hits) — proves the Provider field,
// RemoteID stringification, and URL/CoverURL/Year all map from the search
// entry point too (not just the single-lookup one).
func TestToSearchResult_RealFixture(t *testing.T) {
	var page searchResponse
	loadFixture(t, "search_solo_leveling.json", &page)

	if len(page.Results) != 3 {
		t.Fatalf("fixture has %d results, want 3", len(page.Results))
	}

	got := toSearchResult(page.Results[0].Record)
	want := metadata.SearchResult{
		Provider: "mangaupdates",
		RemoteID: "15180124327",
		Title:    "Solo Leveling",
		URL:      "https://www.mangaupdates.com/series/6z1uqw7/solo-leveling",
		CoverURL: "https://cdn.mangaupdates.com/image/i515366.jpg",
		Year:     2018,
	}
	if got != want {
		t.Errorf("toSearchResult(results[0]) = %+v, want %+v", got, want)
	}

	// Second/third hits: distinct ids, the "(Novel)" and "Ragnarok" spin-off
	// titles a caller must NOT confuse with the primary manhwa.
	got2 := toSearchResult(page.Results[1].Record)
	if got2.RemoteID != "13184758110" || got2.Title != "Solo Leveling (Novel)" || got2.Year != 2016 {
		t.Errorf("second result = %+v, unexpected", got2)
	}
	got3 := toSearchResult(page.Results[2].Record)
	if got3.RemoteID != "47955563021" || got3.Title != "Solo Leveling: Ragnarok" || got3.Year != 2024 {
		t.Errorf("third result = %+v, unexpected", got3)
	}
}

// --- Synthetic edge-case tests -------------------------------------------
//
// These exercise mapping branches the captured live fixtures don't hit
// (a not-completed/hiatus series, an unrated series, an unparseable year,
// blank entries in a collection). The field-name/shape correctness is
// already proven against the real fixtures above; these are pure-function
// branch tests over the already-shape-verified wire structs.

func TestNormalizeStatus_Completed(t *testing.T) {
	if got := normalizeStatus("anything, ignored", true); got != "completed" {
		t.Errorf("normalizeStatus(_, true) = %q, want completed", got)
	}
}

func TestNormalizeStatus_HiatusDetectedFromFreeText(t *testing.T) {
	if got := normalizeStatus("120 Chapters (Hiatus)", false); got != "hiatus" {
		t.Errorf("normalizeStatus(hiatus text, false) = %q, want hiatus", got)
	}
	// Case-insensitive scan.
	if got := normalizeStatus("On HIATUS since 2020", false); got != "hiatus" {
		t.Errorf("normalizeStatus(HIATUS text, false) = %q, want hiatus", got)
	}
}

func TestNormalizeStatus_DefaultsToOngoing(t *testing.T) {
	if got := normalizeStatus("50 Chapters (Ongoing)", false); got != "ongoing" {
		t.Errorf("normalizeStatus(ongoing text, false) = %q, want ongoing", got)
	}
	if got := normalizeStatus("", false); got != "ongoing" {
		t.Errorf("normalizeStatus(empty, false) = %q, want ongoing", got)
	}
}

func TestParseYear(t *testing.T) {
	cases := map[string]int{
		"2018":     2018,
		"2018-1":   2018,
		"":         0,
		"unknown":  0,
		"  2020  ": 2020,
	}
	for in, want := range cases {
		if got := parseYear(in); got != want {
			t.Errorf("parseYear(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestToScore_NilRatingMapsToZero(t *testing.T) {
	if got := toScore(nil); got != 0 {
		t.Errorf("toScore(nil) = %v, want 0", got)
	}
}

func TestToScore_ScalesTenToHundred(t *testing.T) {
	rating := 7.25
	if got := toScore(&rating); got != 72.5 {
		t.Errorf("toScore(7.25) = %v, want 72.5", got)
	}
}

func TestToSeriesMetadata_EmptyCollectionsMapToNil(t *testing.T) {
	got := toSeriesMetadata(seriesDetail{})

	if got.Genres != nil {
		t.Errorf("Genres = %v, want nil for a record with no genres", got.Genres)
	}
	if got.Tags != nil {
		t.Errorf("Tags = %v, want nil for a record with no categories", got.Tags)
	}
	if got.Authors != nil {
		t.Errorf("Authors = %v, want nil for a record with no authors", got.Authors)
	}
	if got.AltTitles != nil {
		t.Errorf("AltTitles = %v, want nil for a record with no associated titles", got.AltTitles)
	}
	if got.Links != nil {
		t.Errorf("Links = %v, want nil for a record with an empty url", got.Links)
	}
}

func TestToAuthors_SkipsEntryWithNoName(t *testing.T) {
	got := toAuthors([]authorEntry{
		{Name: "Real Name", Type: "Author"},
		{Name: "", Type: "Artist"},
	})
	if len(got) != 1 || got[0].Name != "Real Name" {
		t.Errorf("toAuthors(...) = %+v, want exactly the one entry with a non-blank name", got)
	}
}

func TestToAltTitles_SkipsBlankTitle(t *testing.T) {
	got := toAltTitles([]associatedTitle{{Title: "Real Alt"}, {Title: ""}})
	if len(got) != 1 || got[0].Name != "Real Alt" {
		t.Errorf("toAltTitles(...) = %+v, want exactly the one entry with a non-blank title", got)
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
