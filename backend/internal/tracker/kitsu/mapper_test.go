package kitsu

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestKitsuMangaURL covers the slug-present and slug-absent cases.
func TestKitsuMangaURL(t *testing.T) {
	if got := kitsuMangaURL("solo-leveling"); got != "https://kitsu.app/manga/solo-leveling" {
		t.Fatalf("kitsuMangaURL(slug) = %q", got)
	}
	if got := kitsuMangaURL(""); got != "" {
		t.Fatalf("kitsuMangaURL(\"\") = %q, want \"\"", got)
	}
}

// TestKitsuDomainConstants pins the base/token endpoints to kitsu.app —
// kitsu.io is a dead domain (the reported "Kitsu: dead domain" bug) since
// Kitsu's migration; this is a regression guard against the old host
// creeping back in.
func TestKitsuDomainConstants(t *testing.T) {
	if apiBaseURL != "https://kitsu.app/api/edge" {
		t.Fatalf("apiBaseURL = %q, want https://kitsu.app/api/edge", apiBaseURL)
	}
	//nolint:gosec // tokenURL is Kitsu's public OAuth token ENDPOINT URL, not a credential (mirrors client.go's own nolint on the constant).
	if tokenURL != "https://kitsu.app/api/oauth/token" {
		t.Fatalf("tokenURL = %q, want https://kitsu.app/api/oauth/token", tokenURL)
	}
}

// TestToTrackSearchResult_MapsFields pins the Kitsu search-hit → shared
// TrackSearchResult mapping.
func TestToTrackSearchResult_MapsFields(t *testing.T) {
	count := 179
	d := mangaData{
		ID: "7224",
		Attributes: mangaSearchAttrs{
			Slug:           "solo-leveling",
			CanonicalTitle: "Solo Leveling",
			Status:         "finished",
			ChapterCount:   &count,
			PosterImage:    posterImage{Original: "https://cdn.example/sl.jpg"},
		},
	}
	got := toTrackSearchResult(d)
	if got.RemoteID != "7224" || got.Title != "Solo Leveling" || got.TotalChapters != 179 ||
		got.URL != "https://kitsu.app/manga/solo-leveling" || got.CoverURL != d.Attributes.PosterImage.Original ||
		got.Status != "finished" {
		t.Fatalf("toTrackSearchResult mismatch: %+v", got)
	}
}

// TestToTrackSearchResult_NilChapterCount confirms an absent chapterCount
// (an ongoing series Kitsu hasn't totalled) degrades to 0, not a panic.
func TestToTrackSearchResult_NilChapterCount(t *testing.T) {
	got := toTrackSearchResult(mangaData{ID: "1", Attributes: mangaSearchAttrs{ChapterCount: nil}})
	if got.TotalChapters != 0 {
		t.Fatalf("TotalChapters = %d, want 0", got.TotalChapters)
	}
}

// TestToTrackSearchResult_MapsEnrichmentFields pins the Search-Enrichment
// fields (Type/StartDate/Score/Description) Kitsu's search-hit → shared
// TrackSearchResult mapping carries, including the "0".."100" string
// averageRating → float64 parse.
func TestToTrackSearchResult_MapsEnrichmentFields(t *testing.T) {
	d := mangaData{
		ID: "7224",
		Attributes: mangaSearchAttrs{
			CanonicalTitle: "Solo Leveling",
			Subtype:        "manga",
			StartDate:      "2018-03-04",
			AverageRating:  "85.07",
			Synopsis:       "A weak hunter grows stronger.",
		},
	}
	got := toTrackSearchResult(d)
	if got.Type != "manga" || got.StartDate != "2018-03-04" || got.Score != 85.07 ||
		got.Description != "A weak hunter grows stronger." {
		t.Fatalf("toTrackSearchResult enrichment fields = %+v", got)
	}
}

// TestParseAverageRating covers the empty and unparseable degrade-to-zero
// cases parseAverageRating's own doc comment describes.
func TestParseAverageRating(t *testing.T) {
	if got := parseAverageRating(""); got != 0 {
		t.Fatalf("parseAverageRating(\"\") = %v, want 0", got)
	}
	if got := parseAverageRating("not-a-number"); got != 0 {
		t.Fatalf("parseAverageRating(garbage) = %v, want 0", got)
	}
	if got := parseAverageRating("42.5"); got != 42.5 {
		t.Fatalf("parseAverageRating(42.5) = %v, want 42.5", got)
	}
}

// TestToTrackEntry_MapsFieldsAndDates pins the library-entry → shared
// TrackEntry mapping, including RemoteID coming from the manga relationship
// (not a caller-supplied fallback) and date parsing.
func TestToTrackEntry_MapsFieldsAndDates(t *testing.T) {
	rating := 16
	d := libraryEntryData{
		ID: "999",
		Attributes: libraryEntryAttrs{
			Status:       "current",
			Progress:     42,
			RatingTwenty: &rating,
			StartedAt:    "2024-03-15T00:00:00.000Z",
			Private:      true,
		},
		Relationships: libraryEntryRelationships{
			Manga: relationshipRef{Data: resourceRef{ID: "7224", Type: "manga"}},
		},
	}
	included := []includedManga{{ID: "7224", Type: "manga", Attributes: includedMangaAttrs{CanonicalTitle: "Berserk"}}}
	got := toTrackEntry(d, included)
	assertScalarFields(t, got)
	if got.StartDate == nil || !got.StartDate.Equal(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("toTrackEntry.StartDate = %v, want 2024-03-15", got.StartDate)
	}
	if got.FinishDate != nil {
		t.Fatalf("toTrackEntry.FinishDate = %v, want nil (empty finishedAt)", got.FinishDate)
	}
}

// assertScalarFields checks the non-date fields of the mapped entry — extracted
// so the driving test stays under the fleet's per-function complexity budget.
func assertScalarFields(t *testing.T, got tracker.TrackEntry) {
	t.Helper()
	if got.RemoteID != "7224" || got.LibraryID != "999" || got.Title != "Berserk" || got.Status != "current" ||
		got.Progress != 42 || got.Score != 16 || !got.Private {
		t.Fatalf("toTrackEntry mismatch: %+v", got)
	}
}

// TestToTrackEntry_NoRatingIsZeroScore confirms an unrated entry
// (RatingTwenty nil) maps to Score 0, distinguishable in the request
// direction (buildLibraryEntryRequest never sends a rating for a 0 score).
func TestToTrackEntry_NoRatingIsZeroScore(t *testing.T) {
	got := toTrackEntry(libraryEntryData{Attributes: libraryEntryAttrs{RatingTwenty: nil}}, nil)
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0", got.Score)
	}
}

// TestBuildLibraryEntryRequest_OmitsRatingWhenScoreZero confirms
// buildLibraryEntryRequest never fabricates a rating the owner never set.
func TestBuildLibraryEntryRequest_OmitsRatingWhenScoreZero(t *testing.T) {
	req := buildLibraryEntryRequest("", tracker.TrackEntry{RemoteID: "7224", Score: 0}, "555")
	if req.Data.Attributes.RatingTwenty != nil {
		t.Fatalf("RatingTwenty = %v, want nil for a zero score", *req.Data.Attributes.RatingTwenty)
	}
}

// TestBuildLibraryEntryRequest_SetsIdentityAndRelationships pins the
// create/update request body's id/type/relationships shape — the exact
// fields TestClient_SaveEntry_RequestBodyShape (client_test.go) also
// verifies over the wire, kept here as a pure-mapper unit test.
func TestBuildLibraryEntryRequest_SetsIdentityAndRelationships(t *testing.T) {
	req := buildLibraryEntryRequest("999", tracker.TrackEntry{RemoteID: "7224", Status: "current", Progress: 5, Score: 18}, "555")
	assertRequestIdentity(t, req)
	assertRequestRelationships(t, req)
	assertRequestAttributes(t, req)
}

// assertRequestIdentity/assertRequestRelationships/assertRequestAttributes
// split TestBuildLibraryEntryRequest_SetsIdentityAndRelationships's checks
// into focused helpers, keeping the driving test under the fleet's
// per-function cyclomatic-complexity budget.
func assertRequestIdentity(t *testing.T, req libraryEntryWriteRequest) {
	t.Helper()
	if req.Data.ID != "999" || req.Data.Type != "library-entries" {
		t.Fatalf("Data.ID/Type = %q/%q, want 999/library-entries", req.Data.ID, req.Data.Type)
	}
}

func assertRequestRelationships(t *testing.T, req libraryEntryWriteRequest) {
	t.Helper()
	if req.Data.Relationships.User.Data.ID != "555" || req.Data.Relationships.User.Data.Type != "users" {
		t.Fatalf("Relationships.User = %+v", req.Data.Relationships.User)
	}
	if req.Data.Relationships.Media.Data.ID != "7224" || req.Data.Relationships.Media.Data.Type != "manga" {
		t.Fatalf("Relationships.Media = %+v", req.Data.Relationships.Media)
	}
}

func assertRequestAttributes(t *testing.T, req libraryEntryWriteRequest) {
	t.Helper()
	if req.Data.Attributes.Progress != 5 || req.Data.Attributes.Status != "current" {
		t.Fatalf("Attributes = %+v", req.Data.Attributes)
	}
	if req.Data.Attributes.RatingTwenty == nil || *req.Data.Attributes.RatingTwenty != 18 {
		t.Fatalf("Attributes.RatingTwenty = %v, want 18", req.Data.Attributes.RatingTwenty)
	}
}

// TestParseKitsuDate covers the valid, empty, and malformed cases.
func TestParseKitsuDate(t *testing.T) {
	if got := parseKitsuDate(""); got != nil {
		t.Fatalf("parseKitsuDate(\"\") = %v, want nil", got)
	}
	if got := parseKitsuDate("not-a-date"); got != nil {
		t.Fatalf("parseKitsuDate(malformed) = %v, want nil", got)
	}
	got := parseKitsuDate("2023-11-04T00:00:00.000Z")
	if got == nil || !got.Equal(time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("parseKitsuDate(2023-11-04) = %v", got)
	}
}

// TestFormatKitsuDate_RoundTrips confirms formatKitsuDate is
// parseKitsuDate's inverse, and that nil maps to "".
func TestFormatKitsuDate_RoundTrips(t *testing.T) {
	if got := formatKitsuDate(nil); got != "" {
		t.Fatalf("formatKitsuDate(nil) = %q, want \"\"", got)
	}
	tm := time.Date(2022, 1, 9, 0, 0, 0, 0, time.UTC)
	if got := formatKitsuDate(&tm); got == "" {
		t.Fatalf("formatKitsuDate returned empty for a non-nil time")
	} else if parsed := parseKitsuDate(got); parsed == nil || !parsed.Equal(tm) {
		t.Fatalf("formatKitsuDate/parseKitsuDate round-trip mismatch: %q -> %v", got, parsed)
	}
}

// TestMangaCollectionResponse_JSONUnmarshal exercises the wire shapes
// against a representative raw Kitsu search response, catching a
// field-name/type drift a struct-literal-only test would miss.
func TestMangaCollectionResponse_JSONUnmarshal(t *testing.T) {
	raw := []byte(`{"data":[{"id":"7224","attributes":{"slug":"solo-leveling","canonicalTitle":"Solo Leveling","status":"finished","chapterCount":179,"posterImage":{"original":"https://x/y.jpg"}}}]}`)
	var page mangaCollectionResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "7224" {
		t.Fatalf("mangaCollectionResponse = %+v", page)
	}
}

// TestLibraryEntryCollectionResponse_EmptyData confirms the "not tracked"
// shape (an empty data array, never a null) decodes to a zero-length slice,
// not nil-panicking on later len() checks.
func TestLibraryEntryCollectionResponse_EmptyData(t *testing.T) {
	var page libraryEntryCollectionResponse
	if err := json.Unmarshal([]byte(`{"data":[]}`), &page); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("Data = %+v, want empty", page.Data)
	}
}
