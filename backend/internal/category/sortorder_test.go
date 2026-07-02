package category_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// TestCreateAppendsAtEnd is the F3 root-cause proof for Create: a new category
// created WITHOUT an explicit sortOrder must land at max(existing)+1, NOT on the
// colliding default 0 (which would tie with the seeded "Manga").
func TestCreateAppendsAtEnd(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	// The five seeded defaults occupy sort_order 0..4 (after normalization).
	dto, err := svc.Create(ctx, "NSFW", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if dto.SortOrder != 5 {
		t.Fatalf("Create append: sortOrder = %d, want 5 (max 4 + 1)", dto.SortOrder)
	}

	// A second append lands at 6 — never colliding with an existing value.
	dto2, err := svc.Create(ctx, "Webtoon", nil)
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if dto2.SortOrder != 6 {
		t.Fatalf("Create append 2: sortOrder = %d, want 6", dto2.SortOrder)
	}
}

// TestNormalizeSortOrderRepairsCollisionAndReorderWorks is the F3 regression
// proof. It reproduces the deployed collision (a second category created at
// sort_order 0, tying with seeded "Manga"), runs the startup normalization, and
// asserts:
//
//	(a) every category now has a DISTINCT, contiguous sort_order (0..N-1);
//	(b) a reorder that swaps the top two rows' sort_order actually changes the
//	    List order — the swap that was a NO-OP under the tie now moves the row.
func TestNormalizeSortOrderRepairsCollisionAndReorderWorks(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	// Reproduce the deployed collision: force a category onto sort_order 0, tying
	// with the seeded "Manga" (also 0 before normalization). We write the tie
	// directly so the scenario matches the owner's real "NSFW at 0" DB.
	nsfw := client.Category.Create().SetName("NSFW").SetSortOrder(0).SaveX(ctx)
	mangaID, err := category.IDByName(ctx, client, "Manga")
	if err != nil {
		t.Fatalf("IDByName(Manga): %v", err)
	}
	client.Category.UpdateOneID(mangaID).SetSortOrder(0).ExecX(ctx)

	// (a) Startup normalization renumbers to distinct, contiguous values.
	if err := category.NormalizeSortOrder(ctx, client); err != nil {
		t.Fatalf("NormalizeSortOrder: %v", err)
	}
	assertContiguousSortOrder(ctx, t, client)

	// (b) The two former-tied rows now have distinct orders, so a swap moves them.
	assertTopTwoSwap(ctx, t, svc)

	// nsfw still exists (sanity — normalization renumbers, never drops rows).
	if n := client.Category.Query().Where(entcategory.Name("NSFW")).CountX(ctx); n != 1 {
		t.Fatalf("NSFW row lost during normalize: count %d", n)
	}
	_ = nsfw
}

// assertContiguousSortOrder fails the test unless every category has a distinct,
// contiguous sort_order (0..N-1) in (sort_order, name) order.
func assertContiguousSortOrder(ctx context.Context, t *testing.T, client *ent.Client) {
	t.Helper()
	all := client.Category.Query().Order(entcategory.BySortOrder(), entcategory.ByName()).AllX(ctx)
	for i, c := range all {
		if c.SortOrder != i {
			t.Fatalf("normalize: %q sort_order = %d, want %d (contiguous)", c.Name, c.SortOrder, i)
		}
	}
}

// assertTopTwoSwap swaps the top two categories' sort_order (what the FE reorder
// does) and fails the test unless the List order actually flips — the proof that
// a swap over now-distinct values moves the row.
func assertTopTwoSwap(ctx context.Context, t *testing.T, svc *category.Service) {
	t.Helper()
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	top, second := list[0], list[1]
	if top.SortOrder == second.SortOrder {
		t.Fatalf("precondition: top and second share sort_order %d after normalize", top.SortOrder)
	}
	if err := svc.Reorder(ctx, uuid.MustParse(top.ID), second.SortOrder); err != nil {
		t.Fatalf("Reorder top: %v", err)
	}
	if err := svc.Reorder(ctx, uuid.MustParse(second.ID), top.SortOrder); err != nil {
		t.Fatalf("Reorder second: %v", err)
	}
	after, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List after swap: %v", err)
	}
	if after[0].ID != second.ID || after[1].ID != top.ID {
		t.Fatalf("reorder swap did not move rows: got [%s, %s], want [%s, %s]",
			after[0].Name, after[1].Name, second.Name, top.Name)
	}
}
