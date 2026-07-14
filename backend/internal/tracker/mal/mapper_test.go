package mal

import (
	"encoding/json"
	"testing"
	"time"
)

// TestMangaPageURL confirms the constructed page URL shape (MAL's REST API
// returns no url field of its own — see mangaPageURL's doc comment).
func TestMangaPageURL(t *testing.T) {
	if got := mangaPageURL(12345); got != "https://myanimelist.net/manga/12345" {
		t.Fatalf("mangaPageURL(12345) = %q", got)
	}
}

// TestToTrackSearchResult_MapsFields pins the MAL search-hit → shared
// TrackSearchResult mapping.
func TestToTrackSearchResult_MapsFields(t *testing.T) {
	n := mangaSearchNode{
		ID:          887,
		Title:       "Berserk",
		MainPicture: mainPictureData{Large: "https://cdn.example/berserk.jpg"},
		NumChapters: 0,
		Status:      "currently_publishing",
	}
	got := toTrackSearchResult(n)
	if got.RemoteID != "887" || got.Title != "Berserk" || got.URL != "https://myanimelist.net/manga/887" ||
		got.CoverURL != n.MainPicture.Large || got.Status != "currently_publishing" || got.TotalChapters != 0 {
		t.Fatalf("toTrackSearchResult mismatch: %+v", got)
	}
}

// TestToTrackSearchResult_MapsEnrichmentFields pins the Search-Enrichment
// fields (Type/StartDate/Score/Description) MAL's search-hit → shared
// TrackSearchResult mapping carries.
func TestToTrackSearchResult_MapsEnrichmentFields(t *testing.T) {
	n := mangaSearchNode{
		ID:        887,
		Title:     "Berserk",
		MediaType: "manga",
		StartDate: "1989-08-25",
		Mean:      9.4,
		Synopsis:  "A lone swordsman's tale.",
	}
	got := toTrackSearchResult(n)
	if got.Type != "manga" || got.StartDate != "1989-08-25" || got.Score != 9.4 || got.Description != "A lone swordsman's tale." {
		t.Fatalf("toTrackSearchResult enrichment fields = %+v", got)
	}
}

// TestToTrackEntry_MapsFieldsAndDates pins the my_list_status → shared
// TrackEntry mapping, including date parsing.
func TestToTrackEntry_MapsFieldsAndDates(t *testing.T) {
	s := &myListStatus{
		Status:          "reading",
		Score:           8,
		NumChaptersRead: 42,
		StartDate:       "2024-03-15",
		FinishDate:      "",
	}
	got := toTrackEntry("887", s, 364)
	if got.RemoteID != "887" || got.Status != "reading" || got.Score != 8 || got.Progress != 42 || got.TotalChapters != 364 {
		t.Fatalf("toTrackEntry mismatch: %+v", got)
	}
	if got.StartDate == nil || !got.StartDate.Equal(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("toTrackEntry.StartDate = %v, want 2024-03-15", got.StartDate)
	}
	if got.FinishDate != nil {
		t.Fatalf("toTrackEntry.FinishDate = %v, want nil (empty finish_date)", got.FinishDate)
	}
}

// TestParseMALDate covers the valid, empty, and malformed cases.
func TestParseMALDate(t *testing.T) {
	if got := parseMALDate(""); got != nil {
		t.Fatalf("parseMALDate(\"\") = %v, want nil", got)
	}
	if got := parseMALDate("not-a-date"); got != nil {
		t.Fatalf("parseMALDate(malformed) = %v, want nil", got)
	}
	got := parseMALDate("2023-11-04")
	if got == nil || !got.Equal(time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("parseMALDate(2023-11-04) = %v", got)
	}
}

// TestFormatMALDate_RoundTrips confirms formatMALDate is parseMALDate's
// inverse, and that nil maps to "" (MAL's "leave unset" signal).
func TestFormatMALDate_RoundTrips(t *testing.T) {
	if got := formatMALDate(nil); got != "" {
		t.Fatalf("formatMALDate(nil) = %q, want \"\"", got)
	}
	tm := time.Date(2022, 1, 9, 0, 0, 0, 0, time.UTC)
	if got := formatMALDate(&tm); got != "2022-01-09" {
		t.Fatalf("formatMALDate(2022-01-09) = %q", got)
	}
}

// TestMangaDetail_JSONUnmarshal exercises the wire shapes against a
// representative raw MAL response payload, catching a field-name/type drift
// a struct-literal-only test would miss.
func TestMangaDetail_JSONUnmarshal(t *testing.T) {
	raw := []byte(`{
		"id": 887,
		"title": "Berserk",
		"num_chapters": 0,
		"my_list_status": {
			"status": "reading",
			"score": 7,
			"num_chapters_read": 364,
			"start_date": "2021-05-01",
			"finish_date": ""
		}
	}`)
	var d mangaDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if d.MyListStatus == nil {
		t.Fatal("MyListStatus not parsed")
	}
	entry := toTrackEntry("887", d.MyListStatus, d.NumChapters)
	if entry.Status != "reading" || entry.Score != 7 || entry.Progress != 364 || entry.TotalChapters != 0 {
		t.Fatalf("toTrackEntry(from JSON) mismatch: %+v", entry)
	}
}

// TestToTrackEntry_TotalChaptersPopulatedFromDetail is the MAL
// TotalChapters-gap regression test (spec/trackers-oauth-phase3 §6 fix):
// GetEntry's manga-detail response carries num_chapters ALONGSIDE
// my_list_status, and it must land on TrackEntry.TotalChapters — the field
// Phase 4's auto-COMPLETED rule (total>0 && last==total) depends on.
func TestToTrackEntry_TotalChaptersPopulatedFromDetail(t *testing.T) {
	raw := []byte(`{
		"id": 887,
		"title": "Berserk",
		"num_chapters": 380,
		"my_list_status": {
			"status": "reading",
			"score": 7,
			"num_chapters_read": 364,
			"start_date": "2021-05-01",
			"finish_date": ""
		}
	}`)
	var d mangaDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	entry := toTrackEntry("887", d.MyListStatus, d.NumChapters)
	if entry.TotalChapters != 380 {
		t.Fatalf("entry.TotalChapters = %d, want 380 (from the detail's num_chapters)", entry.TotalChapters)
	}
}

// TestMangaDetail_NoListStatus confirms a manga the account has not tracked
// (MAL omits my_list_status entirely) decodes to a nil pointer, not a
// zero-value struct — this is what GetEntry's (nil, nil) branch depends on.
func TestMangaDetail_NoListStatus(t *testing.T) {
	raw := []byte(`{"id": 1, "title": "Untracked Manga", "num_chapters": 10}`)
	var d mangaDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if d.MyListStatus != nil {
		t.Fatalf("MyListStatus = %+v, want nil", d.MyListStatus)
	}
}
