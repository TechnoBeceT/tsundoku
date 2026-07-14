package trackers

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestDefaultScoreFormat pins the per-tracker fallback scale used when a
// binding's TrackerConnection has never captured a score_format (the score-
// scale bug this feature fixes: without this table every tracker rendered
// on a fixed 0-10 scale regardless of its actual native scale). White-box
// (package trackers, not trackers_test) so this pure mapping is verified
// with no DB/Docker dependency — mirrors the codebase's other
// export_test.go-style internal unit tests (e.g. internal/library/importance_test.go).
func TestDefaultScoreFormat(t *testing.T) {
	tests := []struct {
		name      string
		trackerID int
		want      string
	}{
		{"AniList defaults to POINT_100", tracker.IDAniList, "POINT_100"},
		{"MAL is a fixed 0-10 scale", tracker.IDMAL, "MAL"},
		{"Kitsu is a fixed 0-20 scale (ratingTwenty)", tracker.IDKitsu, "KITSU_RATING_TWENTY"},
		{"MangaUpdates has no native score", tracker.IDMangaUpdates, "MANGAUPDATES"},
		{"unknown tracker id has no native scale to report", 9999, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultScoreFormat(tt.trackerID); got != tt.want {
				t.Errorf("defaultScoreFormat(%d) = %q, want %q", tt.trackerID, got, tt.want)
			}
		})
	}
}
