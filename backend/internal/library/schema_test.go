package library_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
)

func TestImportEntry_Migrates(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	e := client.ImportEntry.Create().
		SetPath("/series/Manga/My Series").
		SetTitle("My Series").
		SetCategory("Manga").
		SetChapterCount(3).
		SaveX(ctx)
	if e.Status != "pending" {
		t.Fatalf("default status = %q, want pending", e.Status)
	}
}
