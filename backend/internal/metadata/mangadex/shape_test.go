//go:build metadata_shape

// This file hits the REAL api.mangadex.org over the network to prove the
// response shape mapper.go/client.go decode against, per the Phase-1
// engine plan's DISCOVERY-FIRST rule. It is excluded from the default
// build/test (no `metadata_shape` tag) so the offline gate never depends
// on network access or MangaDex's availability.
//
// Run: go test -tags metadata_shape -run TestShapeMangaDex ./internal/metadata/mangadex/ -v

package mangadex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/mangadex"
)

// TestShapeMangaDex_Search logs the decoded shape of a real MangaDex
// search response and asserts it against the Client's public contract.
func TestShapeMangaDex_Search(t *testing.T) {
	c := mangadex.New(&http.Client{Timeout: 15 * time.Second})
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
		if r.Provider != "mangadex" {
			t.Errorf("Provider = %q, want mangadex", r.Provider)
		}
		if r.RemoteID == "" {
			t.Error("RemoteID is empty")
		}
		if r.Title == "" {
			t.Error("Title is empty")
		}
	}
}

// TestShapeMangaDex_GetSeriesMetadata logs the decoded shape of a real
// MangaDex manga-detail response (One Piece, a stable well-known id) and
// asserts the mapper populated the fields this task's mapper.go claims.
func TestShapeMangaDex_GetSeriesMetadata(t *testing.T) {
	c := mangadex.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const onePieceID = "a1c7c817-4e59-43b7-9365-09675a149a6f"
	meta, err := c.GetSeriesMetadata(ctx, onePieceID)
	if err != nil {
		t.Fatalf("GetSeriesMetadata: %v", err)
	}

	b, _ := json.MarshalIndent(meta, "", "  ")
	t.Logf("GetSeriesMetadata(One Piece) decoded shape:\n%s", b)

	if meta.Title == "" {
		t.Error("Title is empty")
	}
	if meta.Status == "" {
		t.Error("Status is empty")
	}
	if meta.CoverURL == "" {
		t.Error("CoverURL is empty")
	}
	if len(meta.Authors) == 0 {
		t.Error("Authors is empty, want at least Oda Eiichirou")
	}
}

// TestShapeMangaDex_Covers logs the decoded shape of a real MangaDex
// cover-gallery response and asserts the multi-cover gallery the Komf
// brief calls out as MangaDex's richest feature actually returns more
// than one candidate.
func TestShapeMangaDex_Covers(t *testing.T) {
	c := mangadex.New(&http.Client{Timeout: 15 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const onePieceID = "a1c7c817-4e59-43b7-9365-09675a149a6f"
	covers, err := c.Covers(ctx, onePieceID)
	if err != nil {
		t.Fatalf("Covers: %v", err)
	}

	b, _ := json.MarshalIndent(covers, "", "  ")
	t.Logf("Covers(One Piece) decoded shape (%d candidates):\n%s", len(covers), b)

	if len(covers) < 2 {
		t.Errorf("Covers returned %d candidates, want a multi-cover gallery (>=2) for a 100+ volume series", len(covers))
	}
	for _, cand := range covers {
		if cand.SourceKind != "metadata" || cand.SourceRef != "mangadex" {
			t.Errorf("CoverCandidate = %+v, want SourceKind=metadata SourceRef=mangadex", cand)
		}
		if cand.CoverURL == "" {
			t.Errorf("CoverCandidate %+v has empty CoverURL", cand)
		}
	}
}

// TestShapeMangaDex_Match proves Match resolves a well-known title to a
// confident hit against the live API.
func TestShapeMangaDex_Match(t *testing.T) {
	c := mangadex.New(&http.Client{Timeout: 15 * time.Second})
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
