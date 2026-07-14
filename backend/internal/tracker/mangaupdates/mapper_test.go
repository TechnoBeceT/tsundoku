package mangaupdates

import (
	"encoding/json"
	"testing"
)

// TestListStatusLabel covers a known list id and an unrecognized one.
func TestListStatusLabel(t *testing.T) {
	if got := listStatusLabel(0); got != "reading" {
		t.Fatalf("listStatusLabel(0) = %q, want reading", got)
	}
	if got := listStatusLabel(2); got != "complete" {
		t.Fatalf("listStatusLabel(2) = %q, want complete", got)
	}
	if got := listStatusLabel(99); got != "" {
		t.Fatalf("listStatusLabel(99) = %q, want \"\" (unrecognized)", got)
	}
}

// TestToTrackSearchResult_MapsFields pins the MangaUpdates search-hit →
// shared TrackSearchResult mapping.
func TestToTrackSearchResult_MapsFields(t *testing.T) {
	r := searchRecord{
		SeriesID: 12345,
		Title:    "Solo Leveling",
		URL:      "https://www.mangaupdates.com/series/12345",
		Image:    searchCoverImage{URL: searchCoverImageURL{Original: "https://x/y.jpg"}},
		Status:   "Complete",
	}
	got := toTrackSearchResult(r)
	if got.RemoteID != "12345" || got.Title != "Solo Leveling" || got.URL != r.URL ||
		got.CoverURL != r.Image.URL.Original || got.Status != "Complete" {
		t.Fatalf("toTrackSearchResult mismatch: %+v", got)
	}
	if got.TotalChapters != 0 {
		t.Fatalf("TotalChapters = %d, want 0 (MangaUpdates search carries no chapter total)", got.TotalChapters)
	}
}

// TestToTrackSearchResult_MapsEnrichmentFields pins MangaUpdates' own
// DELIBERATELY THIN Search-Enrichment subset (see searchRecord's own doc
// comment): Type/StartDate(year)/Description DO map through (the record
// carries them), but Score stays at its zero value — MangaUpdates' search
// response carries a `bayesian_rating` community score, but neither
// reference client (Komikku/Suwayomi-Server) trusts it as a search-result
// score, and this port follows that same precedent rather than inventing a
// mapping of its own.
func TestToTrackSearchResult_MapsEnrichmentFields(t *testing.T) {
	r := searchRecord{
		SeriesID:    12345,
		Title:       "Solo Leveling",
		Type:        "Manhwa",
		Year:        "2018",
		Description: "A weak hunter grows stronger.",
	}
	got := toTrackSearchResult(r)
	if got.Type != "Manhwa" || got.StartDate != "2018" || got.Description != "A weak hunter grows stronger." {
		t.Fatalf("toTrackSearchResult enrichment fields = %+v", got)
	}
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0 (MangaUpdates search-result score is deliberately left unmapped)", got.Score)
	}
}

// TestToTrackEntry_MapsFields pins the list-series entry → shared
// TrackEntry mapping, including RemoteID/Title coming from the entry's own
// series object and Status resolving from list_id.
func TestToTrackEntry_MapsFields(t *testing.T) {
	e := listSeriesEntry{
		ListID: 0,
		Series: muSeriesRef{ID: 12345, Title: "Solo Leveling"},
		Status: muStatus{Volume: 3, Chapter: 42},
	}
	got := toTrackEntry(e)
	if got.RemoteID != "12345" || got.Title != "Solo Leveling" || got.Status != "reading" || got.Progress != 42 {
		t.Fatalf("toTrackEntry mismatch: %+v", got)
	}
	if got.LibraryID != "" {
		t.Fatalf("LibraryID = %q, want \"\" — MangaUpdates has no separate list-entry id", got.LibraryID)
	}
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0 — a list-series entry alone carries no rating", got.Score)
	}
}

// TestSearchResponse_JSONUnmarshal exercises the wire shapes against a
// representative raw MangaUpdates search response, catching a
// field-name/type drift a struct-literal-only test would miss.
func TestSearchResponse_JSONUnmarshal(t *testing.T) {
	raw := []byte(`{"results":[{"record":{"series_id":12345,"title":"Solo Leveling","url":"https://www.mangaupdates.com/series/12345","image":{"url":{"original":"https://x/y.jpg","thumb":"https://x/y_t.jpg"}},"status":"Complete"}}]}`)
	var page searchResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].Record.SeriesID != 12345 {
		t.Fatalf("searchResponse = %+v", page)
	}
}

// TestListSeriesEntry_JSONUnmarshal exercises the GET /lists/series/{id}
// wire shape — note the nested series object's key is "id", NOT
// "series_id" (that key belongs only to the unrelated /series/search
// response; see muSeriesRef's doc comment).
func TestListSeriesEntry_JSONUnmarshal(t *testing.T) {
	raw := []byte(`{"list_id":0,"series":{"id":12345,"title":"Solo Leveling","url":"https://www.mangaupdates.com/series/12345"},"status":{"volume":0,"chapter":42}}`)
	var e listSeriesEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if e.Series.ID != 12345 || e.Status.Chapter != 42 {
		t.Fatalf("listSeriesEntry = %+v", e)
	}
}
