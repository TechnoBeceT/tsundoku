package providers_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/providers"
)

// TestNewRegistry_ReturnsBothPhase3aTrackers pins the production tracker
// set: exactly AniList + MAL, each addressable by its registry id — the
// registry ids are the load-bearing contract (GET /api/trackers/:id/...,
// spec §4, addresses a tracker by number, not by registration order).
func TestNewRegistry_ReturnsBothPhase3aTrackers(t *testing.T) {
	reg := providers.NewRegistry(providers.Config{
		AniListClientID: "test-anilist-id",
		MALClientID:     "test-mal-id",
	})

	ts := reg.Trackers()
	if len(ts) != 2 {
		t.Fatalf("Trackers() returned %d trackers, want 2", len(ts))
	}

	mal, ok := reg.ByID(tracker.IDMAL)
	if !ok || mal.Key() != "mal" {
		t.Fatalf("ByID(IDMAL) = (%v, %v), want the mal tracker", mal, ok)
	}
	anilist, ok := reg.ByID(tracker.IDAniList)
	if !ok || anilist.Key() != "anilist" {
		t.Fatalf("ByID(IDAniList) = (%v, %v), want the anilist tracker", anilist, ok)
	}
}

// TestNewRegistry_BuildsEvenWithBlankClientIDs confirms a blank client-id
// does NOT fail construction — the "blank disables this tracker" pattern
// means GET /api/trackers (3c) must still be able to list a disabled
// tracker (isLoggedIn=false), never omit it entirely.
func TestNewRegistry_BuildsEvenWithBlankClientIDs(t *testing.T) {
	reg := providers.NewRegistry(providers.Config{})
	if len(reg.Trackers()) != 2 {
		t.Fatalf("Trackers() with blank config = %d, want 2 (both trackers still registered, just disabled)", len(reg.Trackers()))
	}
}
