package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// TestSeriesCategoryEdgeLinksToCategory verifies that a Series created with a
// category_id resolves its category edge to the linked Category row — the
// schema-level replacement for the former fixed category enum.
func TestSeriesCategoryEdgeLinksToCategory(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// testdb seeds the five defaults; link a series to "Manhwa".
	manhwa := client.Category.Query().Where(entcategory.Name("Manhwa")).OnlyX(ctx)

	client.Series.Create().
		SetTitle("Manhwa Series").
		SetSlug("manhwa-series").
		SetCategoryID(manhwa.ID).
		SaveX(ctx)

	got := client.Series.Query().WithCategory().OnlyX(ctx)
	if got.Edges.Category == nil || got.Edges.Category.Name != "Manhwa" {
		t.Fatalf("expected category edge Manhwa, got %+v", got.Edges.Category)
	}
}

// TestCategoryNameIsUnique verifies the Category.name unique constraint: a second
// row with the same name must fail to save.
func TestCategoryNameIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// "Manga" already exists from the seed; creating it again must violate the
	// unique-name constraint.
	if err := client.Category.Create().SetName("Manga").Exec(ctx); err == nil {
		t.Fatal("expected unique-name constraint violation for duplicate category, got nil")
	}
}

// TestEnsureDefaultsSeedsFiveDefaultOther verifies the seed on a fresh DB: exactly
// the five defaults exist with "Other" as the single is_default landing (QCAT-296
// retired the protected flag), and EnsureDefaults is idempotent.
func TestEnsureDefaultsSeedsFiveDefaultOther(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// testdb already seeded; running EnsureDefaults again must remain idempotent.
	if err := category.EnsureDefaults(ctx, client); err != nil {
		t.Fatalf("EnsureDefaults (2nd run): %v", err)
	}

	if n := client.Category.Query().CountX(ctx); n != 5 {
		t.Fatalf("expected 5 seeded categories, got %d", n)
	}
	// Exactly one is_default, and on a fresh seed it is "Other".
	if n := client.Category.Query().Where(entcategory.IsDefault(true)).CountX(ctx); n != 1 {
		t.Fatalf("expected exactly 1 default category, got %d", n)
	}
	other := client.Category.Query().Where(entcategory.Name("Other")).OnlyX(ctx)
	if !other.IsDefault {
		t.Fatal("expected the seeded Other category to be the default")
	}
}
