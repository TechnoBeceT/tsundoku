package trackers

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/anilist"
	"github.com/technobecet/tsundoku/internal/tracker/kitsu"
	"github.com/technobecet/tsundoku/internal/tracker/mal"
	"github.com/technobecet/tsundoku/internal/tracker/mangaupdates"
)

// TestToTrackerDTO_SupportsPrivate pins toTrackerDTO's supportsPrivate
// mapping against every REAL tracker client (not a fake) — white-box
// (package trackers, not trackers_test) so this pure mapping is verified
// with no DB/Docker/network dependency, mirroring
// TestDefaultScoreFormat's own discipline. AniList/Kitsu both carry a
// remote `private` concept (true); MAL/MangaUpdates don't (false) — see
// tracker.Tracker.SupportsPrivate's own doc comment.
func TestToTrackerDTO_SupportsPrivate(t *testing.T) {
	tests := []struct {
		name string
		t    tracker.Tracker
		want bool
	}{
		{"AniList supports private", anilist.New("", nil), true},
		{"Kitsu supports private", kitsu.New(nil), true},
		{"MAL does not support private", mal.New("", "", nil), false},
		{"MangaUpdates does not support private", mangaupdates.New(nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dto := toTrackerDTO(tt.t, nil)
			if dto.SupportsPrivate != tt.want {
				t.Errorf("toTrackerDTO(%s).SupportsPrivate = %v, want %v", tt.t.Key(), dto.SupportsPrivate, tt.want)
			}
		})
	}
}

// TestToTrackSearchResultDTO_RoundTrip confirms every Search-Enrichment
// field (type/startDate/score/description) survives the
// tracker.TrackSearchResult → TrackSearchResultDTO mapping unchanged — the
// wire-facing half of the enrichment slice (the client-side population is
// pinned per-tracker in each tracker/*/client_test.go).
func TestToTrackSearchResultDTO_RoundTrip(t *testing.T) {
	in := tracker.TrackSearchResult{
		RemoteID:      "42",
		Title:         "Solo Leveling",
		URL:           "https://example.test/manga/42",
		CoverURL:      "https://example.test/cover.jpg",
		Status:        "RELEASING",
		TotalChapters: 179,
		Type:          "MANGA",
		StartDate:     "2018",
		Score:         87,
		Description:   "A hunter's story.",
	}
	got := toTrackSearchResultDTO(in)
	want := TrackSearchResultDTO{
		RemoteID:      "42",
		Title:         "Solo Leveling",
		URL:           "https://example.test/manga/42",
		CoverURL:      "https://example.test/cover.jpg",
		Status:        "RELEASING",
		TotalChapters: 179,
		Type:          "MANGA",
		StartDate:     "2018",
		Score:         87,
		Description:   "A hunter's story.",
	}
	if got != want {
		t.Errorf("toTrackSearchResultDTO(%+v) = %+v, want %+v", in, got, want)
	}
}

// TestToTrackSearchResultDTOs_NonNilEmpty confirms an empty result list maps
// to a non-nil (never null-in-JSON) DTO slice.
func TestToTrackSearchResultDTOs_NonNilEmpty(t *testing.T) {
	out := toTrackSearchResultDTOs(nil)
	if out == nil {
		t.Fatal("toTrackSearchResultDTOs(nil) = nil, want a non-nil empty slice")
	}
	if len(out) != 0 {
		t.Fatalf("toTrackSearchResultDTOs(nil) len = %d, want 0", len(out))
	}
}
