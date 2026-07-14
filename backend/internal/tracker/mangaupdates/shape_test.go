//go:build tracker_shape

// This file hits the REAL https://api.mangaupdates.com/v1 tracker-sync
// surface. Build-tagged out of the default `go test ./...` gate (mirrors
// internal/tracker/mal's *_shape convention) and NEVER run in CI — slice 3b
// is DORMANT/credential-gated (no owner MangaUpdates account tonight, spec
// §6). The OWNER runs this once trackers are activated, supplying a real
// MangaUpdates username/password.
//
// Run manually:
//
//	go test -tags tracker_shape -run TestShapeTracker_MangaUpdates ./internal/tracker/mangaupdates/ -v
package mangaupdates_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker/mangaupdates"
)

// TestShapeTracker_MangaUpdates_LoginAndAuthedOps re-proves LoginCredentials
// + Search + GetEntry against the REAL MangaUpdates API — skipped unless
// BOTH TSUNDOKU_TRACKER_TEST_MANGAUPDATES_USERNAME and
// TSUNDOKU_TRACKER_TEST_MANGAUPDATES_PASSWORD are set.
func TestShapeTracker_MangaUpdates_LoginAndAuthedOps(t *testing.T) {
	username := os.Getenv("TSUNDOKU_TRACKER_TEST_MANGAUPDATES_USERNAME")
	password := os.Getenv("TSUNDOKU_TRACKER_TEST_MANGAUPDATES_PASSWORD")
	if username == "" || password == "" {
		t.Skip("TSUNDOKU_TRACKER_TEST_MANGAUPDATES_USERNAME / TSUNDOKU_TRACKER_TEST_MANGAUPDATES_PASSWORD not set — run this manually once a MangaUpdates account is connected")
	}

	c := mangaupdates.New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, err := c.LoginCredentials(ctx, username, password)
	if err != nil {
		t.Fatalf("LoginCredentials: %v", err)
	}
	if tok.Access == "" {
		t.Fatal("LoginCredentials returned an empty session token")
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
	t.Logf("entry (nil = not on the Reading List):\n%s", entryJSON)
}
