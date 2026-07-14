package providers_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/providers"
)

// TestNewRegistry_ReturnsAllFourTrackers pins the production tracker set:
// AniList + MAL + Kitsu + MangaUpdates, each addressable by its registry
// id — the registry ids are the load-bearing contract
// (GET /api/trackers/:id/..., spec §4, addresses a tracker by number, not
// by registration order).
func TestNewRegistry_ReturnsAllFourTrackers(t *testing.T) {
	reg := providers.NewRegistry(providers.Config{
		AniListClientID: "test-anilist-id",
		MALClientID:     "test-mal-id",
	})

	ts := reg.Trackers()
	if len(ts) != 4 {
		t.Fatalf("Trackers() returned %d trackers, want 4", len(ts))
	}

	mal, ok := reg.ByID(tracker.IDMAL)
	if !ok || mal.Key() != "mal" {
		t.Fatalf("ByID(IDMAL) = (%v, %v), want the mal tracker", mal, ok)
	}
	anilist, ok := reg.ByID(tracker.IDAniList)
	if !ok || anilist.Key() != "anilist" {
		t.Fatalf("ByID(IDAniList) = (%v, %v), want the anilist tracker", anilist, ok)
	}
	kitsu, ok := reg.ByID(tracker.IDKitsu)
	if !ok || kitsu.Key() != "kitsu" {
		t.Fatalf("ByID(IDKitsu) = (%v, %v), want the kitsu tracker", kitsu, ok)
	}
	mangaupdates, ok := reg.ByID(tracker.IDMangaUpdates)
	if !ok || mangaupdates.Key() != "mangaupdates" {
		t.Fatalf("ByID(IDMangaUpdates) = (%v, %v), want the mangaupdates tracker", mangaupdates, ok)
	}
}

// TestNewRegistry_BuildsEvenWithBlankClientIDs confirms a blank OAuth
// client-id does NOT fail construction — the "blank disables this tracker"
// pattern means GET /api/trackers (3c) must still be able to list a
// disabled tracker (isLoggedIn=false), never omit it entirely. Kitsu/
// MangaUpdates need no client-id at all, so this also confirms they are
// unconditionally present.
func TestNewRegistry_BuildsEvenWithBlankClientIDs(t *testing.T) {
	reg := providers.NewRegistry(providers.Config{})
	if len(reg.Trackers()) != 4 {
		t.Fatalf("Trackers() with blank config = %d, want 4 (all four trackers still registered, OAuth ones just disabled)", len(reg.Trackers()))
	}
}
