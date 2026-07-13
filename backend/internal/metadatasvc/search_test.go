package metadatasvc_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
)

// TestSearch_DelegatesToRegistry confirms Search is a straight passthrough to
// the Registry's own fan-out (already exhaustively tested in
// internal/metadata) — this only pins that metadatasvc doesn't drop or
// reshape the result.
func TestSearch_DelegatesToRegistry(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	provider := &fakeProvider{
		key: "anilist",
		searchResults: []metadata.SearchResult{
			{Provider: "anilist", RemoteID: "1", Title: "One Piece"},
		},
	}
	registry := metadata.NewRegistry(provider)
	svc := metadatasvc.NewService(db, registry, storage)

	got, err := svc.Search(ctx, "One Piece", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "One Piece" {
		t.Fatalf("Search results = %+v, want the fake provider's one hit", got)
	}
}
