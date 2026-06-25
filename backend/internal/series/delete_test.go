package series_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/series"
)

// seedFullSeries creates a series with one provider (+ a provider chapter + sync
// state), one chapter, and a LatestSeries row, plus a real on-disk folder under
// storage. Returns the series id.
func seedFullSeries(t *testing.T, ctx context.Context, db *ent.Client, storage string) uuid.UUID {
	t.Helper()
	s := db.Series.Create().SetTitle("Doomed").SetSlug("doomed").SetCategory(entseries.CategoryManhwa).SaveX(ctx)
	p := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(p.ID).SetChapterKey("c1").SetNumber(1).SaveX(ctx)
	db.SuwayomiSyncState.Create().SetSeriesProviderID(p.ID).SetState("ok").SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("c1").SetNumber(1).SetState(entchapter.StateDownloaded).SaveX(ctx)
	db.LatestSeries.Create().SetSeriesID(s.ID).SetProvider("mangadex").SetRank(0).SaveX(ctx)
	dir := disk.SeriesDir(storage, "Manhwa", "Doomed")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(dir+"/c1.cbz", []byte("x"), 0o600); err != nil {
		t.Fatalf("seed cbz: %v", err)
	}
	return s.ID
}

// TestDeleteSeries_DeleteFilesTrue removes every DB row AND the disk folder.
func TestDeleteSeries_DeleteFilesTrue(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	id := seedFullSeries(t, ctx, db, storage)
	svc := series.NewService(db, storage, 14)

	if err := svc.DeleteSeries(ctx, id, true); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}

	if n := db.Series.Query().CountX(ctx); n != 0 {
		t.Errorf("series rows = %d, want 0", n)
	}
	if n := db.SeriesProvider.Query().CountX(ctx); n != 0 {
		t.Errorf("provider rows = %d, want 0", n)
	}
	if n := db.ProviderChapter.Query().CountX(ctx); n != 0 {
		t.Errorf("provider-chapter rows = %d, want 0", n)
	}
	if n := db.SuwayomiSyncState.Query().CountX(ctx); n != 0 {
		t.Errorf("sync-state rows = %d, want 0", n)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 0 {
		t.Errorf("chapter rows = %d, want 0", n)
	}
	if n := db.LatestSeries.Query().CountX(ctx); n != 0 {
		t.Errorf("latest-series rows = %d, want 0", n)
	}
	if _, err := os.Stat(disk.SeriesDir(storage, "Manhwa", "Doomed")); !os.IsNotExist(err) {
		t.Errorf("disk folder still present, stat err = %v", err)
	}
}

// TestDeleteSeries_DeleteFilesFalse removes all DB rows but KEEPS the disk folder.
func TestDeleteSeries_DeleteFilesFalse(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	id := seedFullSeries(t, ctx, db, storage)
	svc := series.NewService(db, storage, 14)

	if err := svc.DeleteSeries(ctx, id, false); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}
	if n := db.Series.Query().CountX(ctx); n != 0 {
		t.Errorf("series rows = %d, want 0", n)
	}
	if _, err := os.Stat(disk.SeriesDir(storage, "Manhwa", "Doomed")); err != nil {
		t.Errorf("disk folder must remain when deleteFiles=false, stat err = %v", err)
	}
}

// TestDeleteSeries_NotFound returns the sentinel for an unknown id.
func TestDeleteSeries_NotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.DeleteSeries(ctx, uuid.New(), true); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("DeleteSeries(unknown) = %v, want ErrSeriesNotFound", err)
	}
}

// TestDeleteSeries_NoFolderDeleteFilesTrue succeeds when the series has no folder.
func TestDeleteSeries_NoFolderDeleteFilesTrue(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	s := db.Series.Create().SetTitle("Nofiles").SetSlug("nofiles").SetCategory(entseries.CategoryOther).SaveX(ctx)
	svc := series.NewService(db, storage, 14)
	if err := svc.DeleteSeries(ctx, s.ID, true); err != nil {
		t.Fatalf("DeleteSeries (no folder) = %v, want nil", err)
	}
	if n := db.Series.Query().CountX(ctx); n != 0 {
		t.Errorf("series rows = %d, want 0", n)
	}
}

// TestDeleteSeries_DiskFailureRollsBack proves a disk-removal failure rolls the
// tx back (DB intact). The failure is forced deterministically and root-safely by
// placing a regular FILE where the category directory should be, so os.RemoveAll
// of <storage>/<category>/<title> fails with ENOTDIR (a parent path component is
// not a directory) — no production injection seam required.
func TestDeleteSeries_DiskFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	s := db.Series.Create().SetTitle("Stuck").SetSlug("stuck").SetCategory(entseries.CategoryOther).SaveX(ctx)
	// Make <storage>/Other a FILE, so RemoveSeriesDir(storage,"Other","Stuck") fails.
	if err := os.WriteFile(storage+"/Other", []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	svc := series.NewService(db, storage, 14)

	if err := svc.DeleteSeries(ctx, s.ID, true); err == nil {
		t.Fatal("DeleteSeries = nil, want a disk-removal error")
	}
	if n := db.Series.Query().Where(entseries.IDEQ(s.ID)).CountX(ctx); n != 1 {
		t.Errorf("series row = %d after rollback, want 1 (tx must roll back on disk failure)", n)
	}
}
