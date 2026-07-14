package metadata_test

import (
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// TestMerge_PrimaryScalarGapFilledFromNextProvider asserts that when the
// primary provider leaves a scalar field empty, Merge falls through Order
// to the next provider that has it set.
func TestMerge_PrimaryScalarGapFilledFromNextProvider(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			"anilist":  {Title: "", Description: ""},
			"mangadex": {Title: "Solo Leveling", Description: "A hunter's tale."},
		},
		Order: []string{"anilist", "mangadex"},
	}

	got := metadata.Merge(in)

	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want gap-filled from mangadex", got.Title)
	}
	if got.Description != "A hunter's tale." {
		t.Errorf("Description = %q, want gap-filled from mangadex", got.Description)
	}
}

// TestMerge_SetPrimaryScalarNeverOverridden asserts a scalar the primary
// provider DID set is never replaced by a lower-priority provider's value,
// even when that value differs.
func TestMerge_SetPrimaryScalarNeverOverridden(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			"anilist":  {Title: "Solo Leveling", Status: "ongoing", Year: 2018, Score: 87.5, Publisher: "D&C Media"},
			"mangadex": {Title: "Only I Level Up", Status: "completed", Year: 2016, Score: 70, Publisher: "Someone Else"},
		},
		Order: []string{"anilist", "mangadex"},
	}

	got := metadata.Merge(in)

	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want primary's value unoverridden", got.Title)
	}
	if got.Status != "ongoing" {
		t.Errorf("Status = %q, want primary's value unoverridden", got.Status)
	}
	if got.Year != 2018 {
		t.Errorf("Year = %d, want primary's value unoverridden", got.Year)
	}
	if got.Score != 87.5 {
		t.Errorf("Score = %v, want primary's value unoverridden", got.Score)
	}
	if got.Publisher != "D&C Media" {
		t.Errorf("Publisher = %q, want primary's value unoverridden", got.Publisher)
	}
}

// TestMerge_ZeroYearAndScoreAreUnsetGapFilled proves Year 0 and Score 0 on
// the primary provider are treated as UNSET and gap-filled from a
// lower-priority provider in Order — distinct from the "set scalar not
// overridden" test, which only proves a NON-zero primary wins. An impl that
// skipped the zero-check (always keeping the primary) would leave Year/Score
// at 0 here and fail.
func TestMerge_ZeroYearAndScoreAreUnsetGapFilled(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			"anilist":  {Title: "Solo Leveling", Year: 0, Score: 0},
			"mangadex": {Title: "Solo Leveling", Year: 2020, Score: 80},
		},
		Order: []string{"anilist", "mangadex"},
	}

	got := metadata.Merge(in)

	if got.Year != 2020 {
		t.Errorf("Year = %d, want 2020 (0 is unset, gap-filled from mangadex)", got.Year)
	}
	if got.Score != 80 {
		t.Errorf("Score = %v, want 80 (0 is unset, gap-filled from mangadex)", got.Score)
	}
}

// TestMerge_OrderMetasKeyMismatch pins the documented Order/Metas contract:
// (a) an Order entry with NO matching Metas key is skipped (no panic), and
// (b) a Metas key ABSENT from Order is never merged — its collection data is
// dropped. Both are asserted in one case so a regression in either direction
// (panicking on a missing key, or merging an off-Order provider) fails.
func TestMerge_OrderMetasKeyMismatch(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			// In Order — its data must be merged.
			"anilist": {Title: "Solo Leveling", Genres: []string{"Action"}},
			// NOT in Order — its data must be dropped, never merged.
			"orphan": {Title: "Should Be Ignored", Genres: []string{"Horror"}},
		},
		// "ghost" is in Order but absent from Metas — must be skipped, no panic.
		Order: []string{"anilist", "ghost"},
	}

	got := metadata.Merge(in)

	// (a) The missing "ghost" key did not panic; the present provider merged.
	if got.Title != "Solo Leveling" {
		t.Errorf("Title = %q, want anilist's (ghost key skipped without panic)", got.Title)
	}
	// (b) The off-Order "orphan" provider's genre must NOT appear.
	wantGenres := []string{"Action"}
	if !reflect.DeepEqual(got.Genres, wantGenres) {
		t.Errorf("Genres = %v, want %v (orphan not in Order must be dropped)", got.Genres, wantGenres)
	}
}

// TestMerge_CollectionsUnionedAndDeduped asserts Genres/Tags/AltTitles/
// Authors/Links union across three providers, dedup, and preserve
// first-seen order (walking Order primary-first) — this is the QCAT-228
// "union collections, never single-provider primary-wins" rule.
func TestMerge_CollectionsUnionedAndDeduped(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			"anilist": {
				Genres:    []string{"Action", "Fantasy"},
				Tags:      []string{"Overpowered"},
				AltTitles: []metadata.AltTitle{{Name: "Na Honjaman Level-Up", Type: "ROMAJI", Lang: "ko"}},
				Authors:   []metadata.Author{{Name: "Chugong", Role: "STORY"}},
				Links:     []metadata.Link{{Label: "AniList", URL: "https://anilist.co/1"}},
			},
			"mangadex": {
				// "action" duplicates AniList's "Action" case-insensitively — must not duplicate.
				Genres: []string{"action", "Adventure"},
				Tags:   []string{"Overpowered", "Regression"}, // "Overpowered" is a dup
				AltTitles: []metadata.AltTitle{
					{Name: "na honjaman level-up ", Type: "SYNONYM", Lang: "ko"}, // dup by normalized name
					{Name: "Solo Leveling", Type: "LOCALIZED", Lang: "en"},
				},
				Authors: []metadata.Author{
					{Name: "Chugong", Role: "STORY"}, // exact dup (Name,Role)
					{Name: "Dubu", Role: "ART"},
				},
				Links: []metadata.Link{
					// Same Label as anilist's link above but a DIFFERENT URL — keyed by
					// Label+URL, so this is NOT a dup and both survive.
					{Label: "AniList", URL: "https://anilist.co/DIFFERENT"},
					{Label: "MangaDex", URL: "https://mangadex.org/1"},
				},
			},
			"mal": {
				Genres: []string{"Action", "Fantasy", "Drama"}, // Action/Fantasy dup, Drama new
			},
		},
		Order: []string{"anilist", "mangadex", "mal"},
	}

	got := metadata.Merge(in)

	wantGenres := []string{"Action", "Fantasy", "Adventure", "Drama"}
	if !reflect.DeepEqual(got.Genres, wantGenres) {
		t.Errorf("Genres = %v, want %v", got.Genres, wantGenres)
	}

	wantTags := []string{"Overpowered", "Regression"}
	if !reflect.DeepEqual(got.Tags, wantTags) {
		t.Errorf("Tags = %v, want %v", got.Tags, wantTags)
	}

	wantAltTitles := []metadata.AltTitle{
		{Name: "Na Honjaman Level-Up", Type: "ROMAJI", Lang: "ko"},
		{Name: "Solo Leveling", Type: "LOCALIZED", Lang: "en"},
	}
	if !reflect.DeepEqual(got.AltTitles, wantAltTitles) {
		t.Errorf("AltTitles = %+v, want %+v", got.AltTitles, wantAltTitles)
	}

	wantAuthors := []metadata.Author{
		{Name: "Chugong", Role: "STORY"},
		{Name: "Dubu", Role: "ART"},
	}
	if !reflect.DeepEqual(got.Authors, wantAuthors) {
		t.Errorf("Authors = %+v, want %+v", got.Authors, wantAuthors)
	}

	wantLinks := []metadata.Link{
		{Label: "AniList", URL: "https://anilist.co/1"},
		{Label: "AniList", URL: "https://anilist.co/DIFFERENT"},
		{Label: "MangaDex", URL: "https://mangadex.org/1"},
	}
	if !reflect.DeepEqual(got.Links, wantLinks) {
		t.Errorf("Links = %+v, want %+v", got.Links, wantLinks)
	}
}

// TestMerge_LinksDedupedByLabelAndURL pins the Link dedup key: two links that
// share a Label but point at DIFFERENT URLs must BOTH survive (a Label-only
// key would silently drop the second one — the bug this test guards against),
// while an EXACT (Label,URL) repeat across providers is still deduped,
// first-seen wins.
func TestMerge_LinksDedupedByLabelAndURL(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			"primary": {
				Links: []metadata.Link{
					{Label: "Read Online", URL: "https://asura.example/delta"},
				},
			},
			"secondary": {
				Links: []metadata.Link{
					// Same Label, different URL — must NOT be dropped.
					{Label: "Read Online", URL: "https://comix.example/delta"},
					// Exact repeat of primary's link — must be deduped.
					{Label: "Read Online", URL: "https://asura.example/delta"},
				},
			},
		},
		Order: []string{"primary", "secondary"},
	}

	got := metadata.Merge(in)

	want := []metadata.Link{
		{Label: "Read Online", URL: "https://asura.example/delta"},
		{Label: "Read Online", URL: "https://comix.example/delta"},
	}
	if !reflect.DeepEqual(got.Links, want) {
		t.Errorf("Links = %+v, want %+v", got.Links, want)
	}
}

// TestMerge_CoverURLStaysZero asserts CoverURL is never populated by Merge
// even when every input provider carries one — cover selection is a
// separate, later concern (QCAT-228).
func TestMerge_CoverURLStaysZero(t *testing.T) {
	in := metadata.MergeInput{
		Metas: map[string]metadata.SeriesMetadata{
			"anilist":  {CoverURL: "https://anilist.co/cover.jpg"},
			"mangadex": {CoverURL: "https://mangadex.org/cover.jpg"},
		},
		Order: []string{"anilist", "mangadex"},
	}

	got := metadata.Merge(in)

	if got.CoverURL != "" {
		t.Errorf("CoverURL = %q, want empty (merged independently elsewhere)", got.CoverURL)
	}
}
