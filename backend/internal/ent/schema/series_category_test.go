package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent/series"
)

// TestSeriesCategoryDefaultsToOther verifies that a Series created without an
// explicit category reads back the schema default of "Other" — so existing rows
// and new imports need no data migration to gain the field.
func TestSeriesCategoryDefaultsToOther(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().
		SetTitle("Defaulted Series").
		SetSlug("defaulted-series").
		SaveX(ctx)

	got := client.Series.GetX(ctx, s.ID)
	if got.Category != series.CategoryOther {
		t.Fatalf("expected default category %q, got %q", series.CategoryOther, got.Category)
	}
}

// TestSeriesCategoryExplicitValue verifies that an explicitly set enum constant
// (Manhwa) round-trips through the database unchanged.
func TestSeriesCategoryExplicitValue(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().
		SetTitle("Manhwa Series").
		SetSlug("manhwa-series").
		SetCategory(series.CategoryManhwa).
		SaveX(ctx)

	got := client.Series.GetX(ctx, s.ID)
	if got.Category != series.CategoryManhwa {
		t.Fatalf("expected category %q, got %q", series.CategoryManhwa, got.Category)
	}
}

// TestSeriesCategoryRejectsInvalidValue verifies that Ent's generated enum
// validator fires on save for a value outside the legal set, so the database
// can never hold an illegal category.
func TestSeriesCategoryRejectsInvalidValue(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	_, err := client.Series.Create().
		SetTitle("Bogus Series").
		SetSlug("bogus-series").
		SetCategory(series.Category("Bogus")).
		Save(ctx)
	if err == nil {
		t.Fatal("expected validation error for invalid category, got nil")
	}
}
