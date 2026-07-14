//go:build tracker_shape

// This file hits the REAL https://kitsu.app/api/oauth/token and
// https://kitsu.app/api/edge tracker-sync surface. Build-tagged out of the
// default `go test ./...` gate (mirrors internal/tracker/mal's
// *_shape convention) and NEVER run in CI — slice 3b is DORMANT/
// credential-gated (no owner Kitsu account tonight, spec §6). The OWNER
// runs this once trackers are activated, supplying a real Kitsu
// username/password.
//
// Run manually:
//
//	go test -tags tracker_shape -run TestShapeTracker_Kitsu ./internal/tracker/kitsu/ -v
package kitsu_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker/kitsu"
)

// TestShapeTracker_Kitsu_LoginAndAuthedOps re-proves LoginCredentials +
// Search + GetEntry against the REAL Kitsu API — skipped unless BOTH
// TSUNDOKU_TRACKER_TEST_KITSU_USERNAME and
// TSUNDOKU_TRACKER_TEST_KITSU_PASSWORD are set.
func TestShapeTracker_Kitsu_LoginAndAuthedOps(t *testing.T) {
	username := os.Getenv("TSUNDOKU_TRACKER_TEST_KITSU_USERNAME")
	password := os.Getenv("TSUNDOKU_TRACKER_TEST_KITSU_PASSWORD")
	if username == "" || password == "" {
		t.Skip("TSUNDOKU_TRACKER_TEST_KITSU_USERNAME / TSUNDOKU_TRACKER_TEST_KITSU_PASSWORD not set — run this manually once a Kitsu account is connected")
	}

	c := kitsu.New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, err := c.LoginCredentials(ctx, username, password)
	if err != nil {
		t.Fatalf("LoginCredentials: %v", err)
	}
	if tok.Access == "" {
		t.Fatal("LoginCredentials returned an empty access token")
	}

	results, err := c.Search(ctx, tok.Access, "Berserk")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned zero results for a title known to exist")
	}
	b, _ := json.MarshalIndent(results[0], "", "  ")
	t.Logf("first result:\n%s", b)

	entry, err := c.GetEntry(ctx, tok.Access, results[0].RemoteID)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	entryJSON, _ := json.MarshalIndent(entry, "", "  ")
	t.Logf("entry (nil = not yet tracked):\n%s", entryJSON)
}
