package anilist

import (
	"encoding/json"
	"testing"
	"time"
)

// TestBestTitle_Fallback confirms the English > Romaji > Native preference
// order and that a fully-blank title never panics (returns "").
func TestBestTitle_Fallback(t *testing.T) {
	cases := []struct {
		name string
		in   titleData
		want string
	}{
		{"english wins", titleData{Romaji: "Ro", English: "En", Native: "Na"}, "En"},
		{"romaji fallback", titleData{Romaji: "Ro", Native: "Na"}, "Ro"},
		{"native fallback", titleData{Native: "Na"}, "Na"},
		{"all blank", titleData{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bestTitle(tc.in); got != tc.want {
				t.Fatalf("bestTitle(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestToTrackSearchResult_MapsFields pins the AniList search-hit → shared
// TrackSearchResult mapping against a representative payload.
func TestToTrackSearchResult_MapsFields(t *testing.T) {
	m := mediaSearchItem{
		ID:         12345,
		Title:      titleData{English: "Solo Leveling"},
		CoverImage: coverImageData{Large: "https://cdn.example/cover.jpg"},
		Status:     "RELEASING",
		Chapters:   nil,
		SiteURL:    "https://anilist.co/manga/12345",
	}
	got := toTrackSearchResult(m)
	if got.RemoteID != "12345" || got.Title != "Solo Leveling" || got.URL != m.SiteURL ||
		got.CoverURL != m.CoverImage.Large || got.Status != "RELEASING" || got.TotalChapters != 0 {
		t.Fatalf("toTrackSearchResult mismatch: %+v", got)
	}

	ch := 270
	m.Chapters = &ch
	got = toTrackSearchResult(m)
	if got.TotalChapters != 270 {
		t.Fatalf("toTrackSearchResult TotalChapters = %d, want 270", got.TotalChapters)
	}
}

// TestToTrackSearchResult_MapsEnrichmentFields pins the Search-Enrichment
// fields (Type/StartDate/Score/Description) AniList's search-hit → shared
// TrackSearchResult mapping carries, plus the nil-AverageScore/zero-year
// degradation each falls back to.
func TestToTrackSearchResult_MapsEnrichmentFields(t *testing.T) {
	score := 87
	m := mediaSearchItem{
		ID:           12345,
		Title:        titleData{English: "Solo Leveling"},
		Format:       "MANGA",
		StartDate:    fuzzyDate{Year: intPtr(2018)},
		AverageScore: &score,
		Description:  "A hunter's story.",
	}
	got := toTrackSearchResult(m)
	if got.Type != "MANGA" || got.StartDate != "2018" || got.Score != 87 || got.Description != "A hunter's story." {
		t.Fatalf("toTrackSearchResult enrichment fields = %+v", got)
	}

	// No rating data yet / no start-date year → both degrade to their zero
	// value, never fabricated.
	blank := mediaSearchItem{ID: 1}
	got = toTrackSearchResult(blank)
	if got.Score != 0 || got.StartDate != "" {
		t.Fatalf("toTrackSearchResult with no score/date = %+v, want zero values", got)
	}
}

// intPtr is a small test helper for *int fields (mediaSearchItem.Chapters,
// fuzzyDate.Year/Month/Day, mediaSearchItem.AverageScore).
func intPtr(v int) *int { return &v }

// TestToTrackEntry_MapsFieldsAndDates pins the AniList MediaList entry →
// shared TrackEntry mapping, including the FuzzyDate → time.Time
// conversion.
func TestToTrackEntry_MapsFieldsAndDates(t *testing.T) {
	year, month, day := 2024, 3, 15
	e := &mediaListEntry{
		ID:          987,
		MediaID:     12345,
		Status:      "CURRENT",
		Score:       85,
		Progress:    42,
		Private:     true,
		StartedAt:   fuzzyDate{Year: &year, Month: &month, Day: &day},
		CompletedAt: fuzzyDate{},
	}
	got := toTrackEntry(e)

	if got.RemoteID != "12345" || got.LibraryID != "987" || got.Status != "CURRENT" ||
		got.Score != 85 || got.Progress != 42 || !got.Private {
		t.Fatalf("toTrackEntry mismatch: %+v", got)
	}
	if got.StartDate == nil || !got.StartDate.Equal(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("toTrackEntry.StartDate = %v, want 2024-03-15", got.StartDate)
	}
	if got.FinishDate != nil {
		t.Fatalf("toTrackEntry.FinishDate = %v, want nil (unset FuzzyDate)", got.FinishDate)
	}
}

// TestFuzzyDateToTime_YearOnlyDefaultsToJan1 confirms a FuzzyDate carrying
// only a year (no month/day — AniList allows this) resolves to January 1st
// rather than being dropped.
func TestFuzzyDateToTime_YearOnlyDefaultsToJan1(t *testing.T) {
	year := 2020
	got := fuzzyDateToTime(fuzzyDate{Year: &year})
	if got == nil || !got.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("fuzzyDateToTime(year-only) = %v, want 2020-01-01", got)
	}
}

// TestFuzzyDateToTime_ZeroYearIsNil confirms a FuzzyDate whose Year is the
// zero-value pointer target (AniList's wire shape for "no date set" — every
// field present but 0/null) maps to nil, not January 1st of year 0.
func TestFuzzyDateToTime_ZeroYearIsNil(t *testing.T) {
	zero := 0
	if got := fuzzyDateToTime(fuzzyDate{Year: &zero}); got != nil {
		t.Fatalf("fuzzyDateToTime(year=0) = %v, want nil", got)
	}
	if got := fuzzyDateToTime(fuzzyDate{}); got != nil {
		t.Fatalf("fuzzyDateToTime(zero value) = %v, want nil", got)
	}
}

// TestTimeToFuzzyDateInput_RoundTrips confirms the outgoing mutation-variable
// mapper round-trips a time.Time and maps nil to nil (AniList's "leave this
// date unset" signal).
func TestTimeToFuzzyDateInput_RoundTrips(t *testing.T) {
	tm := time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC)
	got := timeToFuzzyDateInput(&tm)
	if got == nil || got.Year != 2023 || got.Month != 11 || got.Day != 4 {
		t.Fatalf("timeToFuzzyDateInput(2023-11-04) = %+v", got)
	}
	if got := timeToFuzzyDateInput(nil); got != nil {
		t.Fatalf("timeToFuzzyDateInput(nil) = %+v, want nil", got)
	}
}

// TestMediaListEntry_JSONUnmarshal exercises the mediaListEntry shape
// against a representative raw AniList response payload (not just
// hand-built structs) to catch a field-name/type drift a struct literal
// test would miss.
func TestMediaListEntry_JSONUnmarshal(t *testing.T) {
	raw := []byte(`{
		"id": 555,
		"mediaId": 777,
		"status": "COMPLETED",
		"score": 92,
		"progress": 180,
		"private": false,
		"startedAt": {"year": 2022, "month": 1, "day": 10},
		"completedAt": {"year": 2023, "month": 6, "day": 30}
	}`)
	var e mediaListEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	entry := toTrackEntry(&e)
	if entry.RemoteID != "777" || entry.LibraryID != "555" || entry.Status != "COMPLETED" ||
		entry.Score != 92 || entry.Progress != 180 {
		t.Fatalf("toTrackEntry(from JSON) mismatch: %+v", entry)
	}
	if entry.StartDate == nil || entry.FinishDate == nil {
		t.Fatalf("toTrackEntry(from JSON) dates not parsed: %+v", entry)
	}
}
