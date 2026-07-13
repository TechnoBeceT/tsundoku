//go:build tracker_shape

// This file hits the REAL https://graphql.anilist.co tracker-sync surface.
// It is build-tagged out of the default `go test ./...` gate (mirrors
// internal/metadata's *_shape convention) and is NEVER run in CI — Phase 3a
// is DORMANT/config-gated (no owner credentials tonight, spec §6). The
// OWNER runs this once trackers are activated (a real AniList app
// client-id + a live account token), to re-prove these shapes against the
// live API the way TestShapeAniList already does for the metadata half.
//
// Run manually:
//
//	go test -tags tracker_shape -run TestShapeTracker_AniList ./internal/tracker/anilist/ -v
//
// TestShapeTracker_AniList_Search needs no credentials (AniList search is
// public). TestShapeTracker_AniList_AuthedOps additionally needs a live
// account access token in TSUNDOKU_TRACKER_TEST_ANILIST_TOKEN — it SKIPS
// (not fails) when that env var is unset, so this file compiles clean and
// is a no-op in any environment without a connected AniList account.
package anilist_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker/anilist"
)

// TestShapeTracker_AniList_Search re-proves the tracker Search shape
// (queries.go's searchQuery) against the live, unauthenticated AniList API.
func TestShapeTracker_AniList_Search(t *testing.T) {
	c := anilist.New("", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := c.Search(ctx, "", "Solo Leveling")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned zero results for a title known to exist")
	}
	b, _ := json.MarshalIndent(results[0], "", "  ")
	t.Logf("first result:\n%s", b)
}

// TestShapeTracker_AniList_AuthedOps re-proves AccountInfo + GetEntry
// against a REAL logged-in account — skipped unless
// TSUNDOKU_TRACKER_TEST_ANILIST_TOKEN is set, since Phase 3a has no owner
// credentials to run this against tonight (spec §6).
func TestShapeTracker_AniList_AuthedOps(t *testing.T) {
	token := os.Getenv("TSUNDOKU_TRACKER_TEST_ANILIST_TOKEN")
	if token == "" {
		t.Skip("TSUNDOKU_TRACKER_TEST_ANILIST_TOKEN not set — run this manually once an AniList account is connected")
	}

	c := anilist.New("", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := c.AccountInfo(ctx, token)
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	t.Logf("account: %+v", info)
	if info.RemoteUserID == "" {
		t.Fatal("AccountInfo returned no viewer id")
	}

	// A well-known, long-running series almost any account either tracks or
	// does not — either branch of GetEntry (populated or nil) is a valid
	// live shape to log.
	const soloLevelingMediaID = "116778"
	entry, err := c.GetEntry(ctx, token, soloLevelingMediaID)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	b, _ := json.MarshalIndent(entry, "", "  ")
	t.Logf("entry (nil = not yet tracked):\n%s", b)
}
