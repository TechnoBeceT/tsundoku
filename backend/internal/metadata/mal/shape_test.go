//go:build metadata_shape

// This file hits the REAL api.myanimelist.net over the network to prove the
// response shape mapper.go/client.go decode against, per the Phase-1
// engine plan's DISCOVERY-FIRST rule. It is excluded from the default
// build/test (no `metadata_shape` tag) so the offline gate never depends
// on network access, MAL's availability, or a client-id.
//
// MAL requires a registered app client-id on every request (X-MAL-CLIENT-ID
// header) — this test reads it from TSUNDOKU_MAL_CLIENTID so the real value
// is NEVER committed to the repo. Run:
//
//	TSUNDOKU_MAL_CLIENTID=<value> go test -tags metadata_shape -run TestShapeMAL ./internal/metadata/mal/ -v

package mal_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/mal"
)

// shapeClient builds a mal.Client from TSUNDOKU_MAL_CLIENTID, skipping the
// test when the env var is absent (e.g. a CI run that never sets it).
func shapeClient(t *testing.T) *mal.Client {
	t.Helper()
	clientID := os.Getenv("TSUNDOKU_MAL_CLIENTID")
	if clientID == "" {
		t.Skip("TSUNDOKU_MAL_CLIENTID not set — skipping live MAL discovery")
	}
	return mal.New(clientID, &http.Client{Timeout: 15 * time.Second})
}

// TestShapeMAL_Search logs the decoded shape of a real MAL search response
// and asserts it against the Client's public contract.
func TestShapeMAL_Search(t *testing.T) {
	c := shapeClient(t)
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
		if r.Provider != "mal" {
			t.Errorf("Provider = %q, want mal", r.Provider)
		}
		if r.RemoteID == "" {
			t.Error("RemoteID is empty")
		}
		if r.Title == "" {
			t.Error("Title is empty")
		}
	}
}

// TestShapeMAL_GetSeriesMetadata logs the decoded shape of a real MAL manga
// detail response and asserts the mapper populated the fields this task's
// mapper.go claims.
func TestShapeMAL_GetSeriesMetadata(t *testing.T) {
	c := shapeClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := c.Search(ctx, "Solo Leveling", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned zero results, cannot proceed to detail lookup")
	}

	meta, err := c.GetSeriesMetadata(ctx, results[0].RemoteID)
	if err != nil {
		t.Fatalf("GetSeriesMetadata: %v", err)
	}

	b, _ := json.MarshalIndent(meta, "", "  ")
	t.Logf("GetSeriesMetadata(%s) decoded shape:\n%s", results[0].RemoteID, b)

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
		t.Error("Authors is empty, want at least one credited author")
	}
}

// TestShapeMAL_Match proves Match resolves a well-known title to a
// confident hit against the live API.
func TestShapeMAL_Match(t *testing.T) {
	c := shapeClient(t)
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
