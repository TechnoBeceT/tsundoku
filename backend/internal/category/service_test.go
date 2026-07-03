package category_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// catIDByName resolves a seeded category id by name for the tests.
func catIDByName(ctx context.Context, t *testing.T, client *ent.Client, name string) uuid.UUID {
	t.Helper()
	id, err := category.IDByName(ctx, client, name)
	if err != nil {
		t.Fatalf("IDByName(%q): %v", name, err)
	}
	return id
}

// TestCreate verifies a new category is created with the given sort order and a
// duplicate name / invalid name are rejected.
func TestCreate(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	order := 7
	dto, err := svc.Create(ctx, "  Indie Comics  ", &order)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if dto.Name != "Indie Comics" || dto.SortOrder != 7 || dto.Protected || dto.Count != 0 {
		t.Fatalf("Create dto mismatch: %+v", dto)
	}

	// Duplicate name → 409 sentinel.
	if _, err := svc.Create(ctx, "Indie Comics", nil); !errors.Is(err, category.ErrCategoryNameTaken) {
		t.Fatalf("Create dup: want ErrCategoryNameTaken, got %v", err)
	}
	// Invalid name → 400 sentinel.
	if _, err := svc.Create(ctx, "bad/name", nil); !errors.Is(err, category.ErrInvalidCategoryName) {
		t.Fatalf("Create invalid: want ErrInvalidCategoryName, got %v", err)
	}
}

// TestListWithCounts verifies List orders by sort_order then name and reports the
// per-category series count from one grouped aggregate.
func TestListWithCounts(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	mangaID := catIDByName(ctx, t, client, "Manga")
	client.Series.Create().SetTitle("A").SetSlug("a").SetCategoryID(mangaID).SaveX(ctx)
	client.Series.Create().SetTitle("B").SetSlug("b").SetCategoryID(mangaID).SaveX(ctx)

	got, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Five seeded defaults, ordered by sort_order: Manga, Manhwa, Manhua, Comic, Other.
	if len(got) != 5 {
		t.Fatalf("List: want 5, got %d", len(got))
	}
	if got[0].Name != "Manga" || got[0].Count != 2 {
		t.Fatalf("List[0]: want Manga count 2, got %+v", got[0])
	}
	if got[4].Name != "Other" || !got[4].Protected {
		t.Fatalf("List[4]: want Other protected, got %+v", got[4])
	}
}

// TestReorder verifies a DB-only sort_order change and the not-found mapping.
func TestReorder(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	id := catIDByName(ctx, t, client, "Comic")
	if err := svc.Reorder(ctx, id, 99); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if got := client.Category.GetX(ctx, id).SortOrder; got != 99 {
		t.Fatalf("Reorder: sort_order = %d, want 99", got)
	}
	if err := svc.Reorder(ctx, uuid.New(), 1); !errors.Is(err, category.ErrCategoryNotFound) {
		t.Fatalf("Reorder unknown: want ErrCategoryNotFound, got %v", err)
	}
}

// TestDelete verifies an empty category is deletable, a non-empty one is 409, and
// the protected default is 400.
func TestDelete(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	// Empty user category → deletable.
	created, err := svc.Create(ctx, "Temp", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tempID := uuid.MustParse(created.ID)
	if err := svc.Delete(ctx, tempID); err != nil {
		t.Fatalf("Delete empty: %v", err)
	}
	if n := client.Category.Query().Where(entcategory.IDEQ(tempID)).CountX(ctx); n != 0 {
		t.Fatalf("Delete empty: row still present")
	}

	// Non-empty → 409.
	mangaID := catIDByName(ctx, t, client, "Manga")
	client.Series.Create().SetTitle("X").SetSlug("x").SetCategoryID(mangaID).SaveX(ctx)
	if err := svc.Delete(ctx, mangaID); !errors.Is(err, category.ErrCategoryNotEmpty) {
		t.Fatalf("Delete non-empty: want ErrCategoryNotEmpty, got %v", err)
	}

	// Default Other → 400 (the current default can never be deleted).
	otherID := catIDByName(ctx, t, client, "Other")
	if err := svc.Delete(ctx, otherID); !errors.Is(err, category.ErrCategoryIsDefault) {
		t.Fatalf("Delete default: want ErrCategoryIsDefault, got %v", err)
	}

	// Unknown → 404.
	if err := svc.Delete(ctx, uuid.New()); !errors.Is(err, category.ErrCategoryNotFound) {
		t.Fatalf("Delete unknown: want ErrCategoryNotFound, got %v", err)
	}
}

// TestSetDefaultMakesPreviousDefaultDeletable is the F2/F4 proof: the seeded
// default is "Other"; after SetDefault promotes another category, exactly one
// default remains, and the demoted "Other" (empty) becomes deletable.
func TestSetDefaultMakesPreviousDefaultDeletable(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	otherID := catIDByName(ctx, t, client, "Other")
	mangaID := catIDByName(ctx, t, client, "Manga")

	// Seeded default is "Other".
	def, err := category.ResolveDefault(ctx, client)
	if err != nil {
		t.Fatalf("ResolveDefault (seeded): %v", err)
	}
	if def.Name != "Other" {
		t.Fatalf("seeded default = %q, want Other", def.Name)
	}

	// Promote "Manga".
	if err := svc.SetDefault(ctx, mangaID); err != nil {
		t.Fatalf("SetDefault(Manga): %v", err)
	}
	// Exactly one default, and it is Manga.
	if n := client.Category.Query().Where(entcategory.IsDefault(true)).CountX(ctx); n != 1 {
		t.Fatalf("want exactly 1 default after SetDefault, got %d", n)
	}
	def2, err := category.ResolveDefault(ctx, client)
	if err != nil || def2.ID != mangaID {
		t.Fatalf("ResolveDefault after promote = %+v (err %v), want Manga", def2, err)
	}

	// "Manga" (the new default) can NOT be deleted.
	if err := svc.Delete(ctx, mangaID); !errors.Is(err, category.ErrCategoryIsDefault) {
		t.Fatalf("Delete new default: want ErrCategoryIsDefault, got %v", err)
	}
	// "Other" (demoted, empty) CAN now be deleted.
	if err := svc.Delete(ctx, otherID); err != nil {
		t.Fatalf("Delete demoted Other: %v", err)
	}
}

// TestSetDefaultUnknownID verifies SetDefault maps a missing id to
// ErrCategoryNotFound (→ 404).
func TestSetDefaultUnknownID(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	if err := svc.SetDefault(ctx, uuid.New()); !errors.Is(err, category.ErrCategoryNotFound) {
		t.Fatalf("SetDefault(unknown): want ErrCategoryNotFound, got %v", err)
	}
}

// TestRenameMovesFolderAndUpdatesDB is the disk↔DB consistency proof for a
// category rename: the on-disk category folder is moved AND the DB name updated.
func TestRenameMovesFolderAndUpdatesDB(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	svc := category.NewService(client, storage)

	// A series filed under "Manga" with a real on-disk folder.
	mangaID := catIDByName(ctx, t, client, "Manga")
	client.Series.Create().SetTitle("Berserk").SetSlug("berserk").SetCategoryID(mangaID).SaveX(ctx)
	seriesDir := filepath.Join(storage, "Manga", "Berserk")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	if err := svc.Rename(ctx, mangaID, "Japanese Manga"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	// DB name updated.
	if got := client.Category.GetX(ctx, mangaID).Name; got != "Japanese Manga" {
		t.Fatalf("Rename: DB name = %q, want Japanese Manga", got)
	}
	// Folder moved.
	if _, err := os.Stat(filepath.Join(storage, "Manga")); !os.IsNotExist(err) {
		t.Fatalf("old category dir should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, "Japanese Manga", "Berserk")); err != nil {
		t.Fatalf("series should have moved with the category: %v", err)
	}
}

// TestRenameProtectedAndConflicts verifies the rename guards: protected default
// → 400, duplicate name → 409, unknown id → 404, invalid name → 400.
func TestRenameProtectedAndConflicts(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	otherID := catIDByName(ctx, t, client, "Other")
	if err := svc.Rename(ctx, otherID, "Misc"); !errors.Is(err, category.ErrCategoryProtected) {
		t.Fatalf("Rename protected: want ErrCategoryProtected, got %v", err)
	}

	mangaID := catIDByName(ctx, t, client, "Manga")
	if err := svc.Rename(ctx, mangaID, "Manhwa"); !errors.Is(err, category.ErrCategoryNameTaken) {
		t.Fatalf("Rename to taken: want ErrCategoryNameTaken, got %v", err)
	}
	if err := svc.Rename(ctx, mangaID, "bad/name"); !errors.Is(err, category.ErrInvalidCategoryName) {
		t.Fatalf("Rename invalid: want ErrInvalidCategoryName, got %v", err)
	}
	if err := svc.Rename(ctx, uuid.New(), "Whatever"); !errors.Is(err, category.ErrCategoryNotFound) {
		t.Fatalf("Rename unknown: want ErrCategoryNotFound, got %v", err)
	}
}

// TestRenameNoFolderIsDBOnly verifies that renaming a category with no on-disk
// folder (no series rendered) updates only the DB.
func TestRenameNoFolderIsDBOnly(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	svc := category.NewService(client, storage)

	created, err := svc.Create(ctx, "Doujin", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := uuid.MustParse(created.ID)
	if err := svc.Rename(ctx, id, "Fan Comics"); err != nil {
		t.Fatalf("Rename no folder: %v", err)
	}
	if got := client.Category.GetX(ctx, id).Name; got != "Fan Comics" {
		t.Fatalf("Rename no folder: DB name = %q, want Fan Comics", got)
	}
	if _, err := os.Stat(filepath.Join(storage, "Fan Comics")); !os.IsNotExist(err) {
		t.Fatalf("no folder should have been created, stat err = %v", err)
	}
}

// TestGet verifies Get returns the persisted state with the live count.
func TestGet(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := category.NewService(client, t.TempDir())

	mangaID := catIDByName(ctx, t, client, "Manga")
	client.Series.Create().SetTitle("Y").SetSlug("y").SetCategoryID(mangaID).SaveX(ctx)

	dto, err := svc.Get(ctx, mangaID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if dto.Name != "Manga" || dto.Count != 1 {
		t.Fatalf("Get: %+v, want Manga count 1", dto)
	}
	if _, err := svc.Get(ctx, uuid.New()); !errors.Is(err, category.ErrCategoryNotFound) {
		t.Fatalf("Get unknown: want ErrCategoryNotFound, got %v", err)
	}
}
