//go:build metadata_shape

package anilist

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestShapeAniList is a DISCOVERY test, not a correctness gate: it hits the
// REAL https://graphql.anilist.co endpoint with the exact searchQuery /
// byIDQuery this package sends and logs the decoded response, so a human
// can confirm the field shapes mapper.go assumes are still accurate before
// trusting it. It is build-tagged out of the default `go test ./...` gate
// (see the repo architecture notes' metadata_shape convention) and is NEVER
// run in CI. Run manually with:
//
//	go test -tags metadata_shape -run TestShapeAniList ./internal/metadata/anilist/ -v
//
// This exact run (2026-07-13) reached AniList successfully; the captured
// output was saved as testdata/media_by_id_solo_leveling.json and
// testdata/search_solo_leveling.json, which mapper_test.go asserts against.
func TestShapeAniList(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	c := &Client{http: client}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var searchData searchPageData
	if err := c.do(ctx, searchQuery, map[string]any{"search": "Solo Leveling", "perPage": 2}, &searchData); err != nil {
		t.Fatalf("search query: %v", err)
	}
	searchJSON, _ := json.MarshalIndent(searchData, "", "  ")
	t.Logf("search response:\n%s", searchJSON)
	if len(searchData.Page.Media) == 0 {
		t.Fatal("search returned zero results for a title known to exist")
	}

	id := searchData.Page.Media[0].ID
	var mediaResult mediaData
	if err := c.do(ctx, byIDQuery, map[string]any{"id": id}, &mediaResult); err != nil {
		t.Fatalf("by-id query: %v", err)
	}
	mediaJSON, _ := json.MarshalIndent(mediaResult, "", "  ")
	t.Logf("by-id response:\n%s", mediaJSON)

	if mediaResult.Media.Title.English == "" && mediaResult.Media.Title.Romaji == "" {
		t.Error("by-id response has no usable title in either english or romaji")
	}
}
