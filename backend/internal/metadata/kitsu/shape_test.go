//go:build metadata_shape

// This file hits the REAL kitsu.io/api/edge over the network to prove the
// JSON:API response shape mapper.go/client.go decode against — Kitsu has NO
// Komf reference (unlike anilist/mangadex, which port an existing Komf
// provider), so this discovery run is what the fresh mapper in this package
// was actually written against; the responses it logs were captured into
// testdata/*.json and drive mapper_test.go offline. Excluded from the
// default build/test (no `metadata_shape` tag) so the offline gate never
// depends on network access or Kitsu's availability.
//
// Run: go test -tags metadata_shape -run TestShapeKitsu ./internal/metadata/kitsu/ -v

package kitsu_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/kitsu"
)

// TestShapeKitsu_Search logs the decoded shape of a real Kitsu search
// response and asserts it against the Client's public contract.
func TestShapeKitsu_Search(t *testing.T) {
	c := kitsu.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := c.Search(ctx, "one piece", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned zero results for a known-real title")
	}

	b, _ := json.MarshalIndent(results, "", "  ")
	t.Logf("Search(\"one piece\") decoded shape:\n%s", b)

	for _, r := range results {
		if r.Provider != "kitsu" {
			t.Errorf("Provider = %q, want kitsu", r.Provider)
		}
		if r.RemoteID == "" {
			t.Error("RemoteID is empty")
		}
		if r.Title == "" {
			t.Error("Title is empty")
		}
	}
}

// TestShapeKitsu_GetSeriesMetadata logs the decoded shape of a real Kitsu
// manga-detail-with-included-categories response (One Piece, id "38", a
// stable well-known id) and asserts the mapper populated the fields this
// task's mapper.go claims — most importantly Genres, which depends on
// correctly joining the categories relationship against the top-level
// `included` array.
func TestShapeKitsu_GetSeriesMetadata(t *testing.T) {
	c := kitsu.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const onePieceID = "38"
	meta, err := c.GetSeriesMetadata(ctx, onePieceID)
	if err != nil {
		t.Fatalf("GetSeriesMetadata: %v", err)
	}

	b, _ := json.MarshalIndent(meta, "", "  ")
	t.Logf("GetSeriesMetadata(One Piece) decoded shape:\n%s", b)

	if meta.Title == "" {
		t.Error("Title is empty")
	}
	if meta.Status != "ongoing" {
		t.Errorf("Status = %q, want ongoing (Kitsu status=current)", meta.Status)
	}
	if meta.CoverURL == "" {
		t.Error("CoverURL is empty")
	}
	if len(meta.Genres) == 0 {
		t.Error("Genres is empty, want the resolved `included` categories (e.g. Action, Comedy)")
	}
	if meta.Score <= 0 {
		t.Errorf("Score = %v, want a parsed positive averageRating", meta.Score)
	}
}

// TestShapeKitsu_Match proves Match resolves a well-known title to a
// confident hit against the live API.
func TestShapeKitsu_Match(t *testing.T) {
	c := kitsu.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := c.Match(ctx, metadata.MatchQuery{Title: "One Piece"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result == nil {
		t.Fatal("Match returned nil for a well-known exact title")
	}
	t.Logf("Match(\"One Piece\") -> %+v", *result)
}

// TestShapeKitsu_GetSeriesCover proves cover bytes actually download from
// the posterImage.original URL the mapper resolves.
func TestShapeKitsu_GetSeriesCover(t *testing.T) {
	c := kitsu.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const onePieceID = "38"
	data, ext, err := c.GetSeriesCover(ctx, onePieceID)
	if err != nil {
		t.Fatalf("GetSeriesCover: %v", err)
	}
	if len(data) == 0 {
		t.Error("GetSeriesCover returned zero bytes")
	}
	if ext == "" {
		t.Error("GetSeriesCover returned an empty ext")
	}
	t.Logf("GetSeriesCover(One Piece) -> %d bytes, ext=%q", len(data), ext)
}
