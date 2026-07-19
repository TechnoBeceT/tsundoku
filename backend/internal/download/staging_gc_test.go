// Package download_test — startup staging-GC proof (FIX 3).
//
// GCStagingRoot is the boot-time backstop that reclaims leaked page-staging dirs:
// a dir survives ONLY while its ProviderChapter's chapter is still downloading
// (wanted/failed/downloading/upgrade_available/upgrading); dirs for completed,
// permanently-failed, or deleted (provider/series gone) chapters are removed.
//
// Requires Docker (via testcontainers).
package download_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// mkStagingDir creates <root>/<name>/ with a single staged page so the GC has a
// non-empty dir to reclaim, and returns its path.
func mkStagingDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir staging dir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0000.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write staged page: %v", err)
	}
	return dir
}

// TestGCStagingRoot proves the keep/remove split: a still-wanted chapter's staging
// dir is KEPT, while a downloaded chapter's dir and an orphan dir (no matching
// ProviderChapter at all) are REMOVED.
func TestGCStagingRoot(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	root := mustTempDir(t)

	s := client.Series.Create().SetTitle("GC").SetSlug("gc").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)

	// A wanted chapter → its staging dir must be KEPT.
	pcWanted := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SetState(entchapter.StateWanted).SaveX(ctx)

	// A downloaded chapter → its staging dir must be REMOVED (bytes already in CBZ).
	pcDownloaded := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c2").
		SetURL("/ch/c2").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("c2").SetState(entchapter.StateDownloaded).SaveX(ctx)

	keptDir := mkStagingDir(t, root, pcWanted.ID.String())
	downloadedDir := mkStagingDir(t, root, pcDownloaded.ID.String())
	orphanDir := mkStagingDir(t, root, uuid.New().String()) // no ProviderChapter at all

	removed, err := download.GCStagingRoot(ctx, client, root)
	if err != nil {
		t.Fatalf("GCStagingRoot: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (downloaded + orphan)", removed)
	}
	if _, err := os.Stat(keptDir); err != nil {
		t.Errorf("wanted chapter's staging dir was removed (want kept): %v", err)
	}
	if _, err := os.Stat(downloadedDir); !os.IsNotExist(err) {
		t.Errorf("downloaded chapter's staging dir survived (want removed); stat err = %v", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("orphan staging dir survived (want removed); stat err = %v", err)
	}
}

// TestGCStagingRoot_AbsentRootIsNoError proves a never-staged deployment (no
// staging root on disk yet) is a clean no-op, not a startup error.
func TestGCStagingRoot_AbsentRootIsNoError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	removed, err := download.GCStagingRoot(ctx, client, filepath.Join(mustTempDir(t), "never-created"))
	if err != nil {
		t.Fatalf("GCStagingRoot on absent root: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}
