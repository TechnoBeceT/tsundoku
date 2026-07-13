//go:build tracker_shape

// This file hits the REAL https://api.myanimelist.net/v2 tracker-sync
// surface AND https://myanimelist.net/v1/oauth2/token. Build-tagged out of
// the default `go test ./...` gate (mirrors internal/metadata's *_shape
// convention) and NEVER run in CI — Phase 3a is DORMANT/config-gated (no
// owner credentials tonight, spec §6). The OWNER runs this once trackers
// are activated (a real MAL app client-id + a live account access token —
// obtaining the token itself requires a real browser OAuth round-trip,
// which this test cannot automate, so it takes the token as an env var
// rather than performing the login).
//
// Run manually:
//
//	go test -tags tracker_shape -run TestShapeTracker_MAL ./internal/tracker/mal/ -v
package mal_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker/mal"
)

// TestShapeTracker_MAL_AuthedOps re-proves Search + GetEntry against the
// REAL MAL v2 API with a live account token — skipped unless BOTH
// TSUNDOKU_TRACKER_TEST_MAL_CLIENT_ID and TSUNDOKU_TRACKER_TEST_MAL_TOKEN
// are set (MAL's REST API requires the app client-id even when a Bearer
// token is also present, per this package's doc comment), since Phase 3a
// has no owner credentials to run this against tonight.
func TestShapeTracker_MAL_AuthedOps(t *testing.T) {
	clientID := os.Getenv("TSUNDOKU_TRACKER_TEST_MAL_CLIENT_ID")
	token := os.Getenv("TSUNDOKU_TRACKER_TEST_MAL_TOKEN")
	if clientID == "" || token == "" {
		t.Skip("TSUNDOKU_TRACKER_TEST_MAL_CLIENT_ID / TSUNDOKU_TRACKER_TEST_MAL_TOKEN not set — run this manually once a MAL account is connected")
	}

	c := mal.New(clientID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := c.Search(ctx, token, "Berserk")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned zero results for a title known to exist")
	}
	b, _ := json.MarshalIndent(results[0], "", "  ")
	t.Logf("first result:\n%s", b)

	entry, err := c.GetEntry(ctx, token, results[0].RemoteID)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	entryJSON, _ := json.MarshalIndent(entry, "", "  ")
	t.Logf("entry (nil = not yet tracked):\n%s", entryJSON)
}
