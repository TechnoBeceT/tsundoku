//go:build metadata_shape

// This file hits the REAL api.mangaupdates.com over the network to prove
// the response shape mapper.go/client.go decode against, per the Phase-1
// engine plan's DISCOVERY-FIRST rule. It is excluded from the default
// build/test (no `metadata_shape` tag) so the offline gate never depends on
// network access or MangaUpdates' availability.
//
// Run: go test -tags metadata_shape -run TestShapeMangaUpdates ./internal/metadata/mangaupdates/ -v
//
// This exact run (2026-07-13) reached MangaUpdates successfully; the
// captured output was saved as testdata/search_solo_leveling.json (search
// response trimmed to its first 3 real results) and
// testdata/series_solo_leveling.json (full real GET /series/{id} response),
// which mapper_test.go asserts against.

package mangaupdates_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/mangaupdates"
)

// TestShapeMangaUpdates_Search logs the decoded shape of a real
// MangaUpdates search response and asserts it against the Client's public
// contract.
func TestShapeMangaUpdates_Search(t *testing.T) {
	c := mangaupdates.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := c.Search(ctx, "Solo Leveling", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned zero results for a known-real title")
	}

	b, _ := json.MarshalIndent(results, "", "  ")
	t.Logf("Search(\"Solo Leveling\") decoded shape:\n%s", b)

	for _, r := range results {
		if r.Provider != "mangaupdates" {
			t.Errorf("Provider = %q, want mangaupdates", r.Provider)
		}
		if r.RemoteID == "" {
			t.Error("RemoteID is empty")
		}
		if r.Title == "" {
			t.Error("Title is empty")
		}
	}
}

// TestShapeMangaUpdates_GetSeriesMetadata logs the decoded shape of a real
// MangaUpdates series-detail response (Solo Leveling, a stable well-known
// id) and asserts the mapper populated the fields this task's mapper.go
// claims.
func TestShapeMangaUpdates_GetSeriesMetadata(t *testing.T) {
	c := mangaupdates.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const soloLevelingID = "15180124327"
	meta, err := c.GetSeriesMetadata(ctx, soloLevelingID)
	if err != nil {
		t.Fatalf("GetSeriesMetadata: %v", err)
	}

	b, _ := json.MarshalIndent(meta, "", "  ")
	t.Logf("GetSeriesMetadata(Solo Leveling) decoded shape:\n%s", b)

	if meta.Title == "" {
		t.Error("Title is empty")
	}
	if meta.Status != "completed" {
		t.Errorf("Status = %q, want completed (Solo Leveling has finished)", meta.Status)
	}
	if meta.CoverURL == "" {
		t.Error("CoverURL is empty")
	}
	if len(meta.Authors) == 0 {
		t.Error("Authors is empty, want at least Chugong")
	}
	if len(meta.AltTitles) == 0 {
		t.Error("AltTitles is empty, want the associated-title list")
	}
	if meta.Score <= 0 {
		t.Errorf("Score = %v, want > 0 for a heavily-rated series", meta.Score)
	}
}

// TestShapeMangaUpdates_Match proves Match resolves a well-known title to a
// confident hit against the live API.
func TestShapeMangaUpdates_Match(t *testing.T) {
	c := mangaupdates.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := c.Match(ctx, metadata.MatchQuery{Title: "Solo Leveling"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result == nil {
		t.Fatal("Match returned nil for a well-known exact title")
	}
	t.Logf("Match(\"Solo Leveling\") -> %+v", *result)
}
