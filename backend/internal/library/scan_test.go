package library_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/importentry"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/sse"
)

// writeKaizokuSeries writes N Kaizoku-style CBZs for one series under
// <storage>/<category>/<title>/. Each CBZ carries an embedded ComicInfo.xml
// (Series/Number/Publisher/Translator) and a Kaizoku filename bracket
// ([provider-scanlator][en] <title> <k>.cbz) — mirrors the zip approach in
// internal/disk/import_test.go (Task 2), repeated here so this package does
// not import the disk test package.
func writeKaizokuSeries(t *testing.T, storage, category, title, provider, scanlator string, n int) {
	t.Helper()
	dir := filepath.Join(storage, category, title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for k := 1; k <= n; k++ {
		number := fmt.Sprintf("%d", k)
		filename := fmt.Sprintf("[%s-%s][en] %s %d.cbz", provider, scanlator, title, k)

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		page, _ := zw.Create("001.jpg")
		_, _ = page.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9}) // minimal jpeg-ish bytes
		ciw, _ := zw.Create("ComicInfo.xml")
		_, _ = ciw.Write([]byte(`<?xml version="1.0"?><ComicInfo><Series>` + title +
			`</Series><Number>` + number + `</Number><Publisher>` + provider +
			`</Publisher><Translator>` + scanlator + `</Translator></ComicInfo>`))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filename), buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScan_StagesFoundSeries(t *testing.T) {
	storage := t.TempDir()
	// reuse the disk-test helper shape: one Kaizoku series, 2 chapters.
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)

	client := testdb.New(t)
	svc := library.NewService(client, nil, nil, nil, func() {}, storage, sse.NewHub())
	ctx := context.Background()

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	assertFoundSeries(t, found[0])
	assertStagedCount(t, client, ctx, 1)

	// Re-scan is idempotent (upsert by path).
	if _, err := svc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	assertStagedCount(t, client, ctx, 1)
}

// assertFoundSeries checks the staged DTO shape for the single "My Series"
// fixture written by writeKaizokuSeries above.
func assertFoundSeries(t *testing.T, f library.FoundSeriesDTO) {
	t.Helper()
	if f.Title != "My Series" || f.Category != "Manga" || f.ChapterCount != 2 {
		t.Fatalf("bad found: %+v", f)
	}
	if f.Status != "pending" || f.AlreadyInDB {
		t.Fatalf("status=%q alreadyInDb=%v, want pending/false", f.Status, f.AlreadyInDB)
	}
	if len(f.Providers) != 1 || f.Providers[0] != "mangadex" {
		t.Fatalf("providers=%v, want [mangadex]", f.Providers)
	}
}

// assertStagedCount checks the number of persisted ImportEntry rows.
func assertStagedCount(t *testing.T, client *ent.Client, ctx context.Context, want int) {
	t.Helper()
	if n := client.ImportEntry.Query().CountX(ctx); n != want {
		t.Fatalf("staged rows = %d, want %d", n, want)
	}
}

// statusForPath returns the persisted ImportEntry.status for a given path key.
func statusForPath(t *testing.T, client *ent.Client, ctx context.Context, path string) string {
	t.Helper()
	e, err := client.ImportEntry.Query().Where(importentry.Path(path)).Only(ctx)
	if err != nil {
		t.Fatalf("query import entry for %q: %v", path, err)
	}
	return e.Status
}

func TestScan_MarksImportedWhenSeriesExists(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)

	client := testdb.New(t)
	ctx := context.Background()
	// A Series whose slug matches the on-disk title already exists in the DB.
	client.Series.Create().
		SetTitle("My Series").
		SetSlug(disk.Slugify("My Series")).
		SaveX(ctx)

	svc := library.NewService(client, nil, nil, nil, func() {}, storage, sse.NewHub())
	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	f := found[0]
	if f.Status != "imported" || !f.AlreadyInDB {
		t.Fatalf("status=%q alreadyInDb=%v, want imported/true", f.Status, f.AlreadyInDB)
	}
}

// TestScan_DowngradesRemovedSeriesToPending is the Slice R regression test
// (spec/library-match-and-source-management, plan/library-match-backend Task
// 0): a series marked "imported" whose Series row has since been deleted (or
// was never actually created, e.g. after a failed import) must downgrade back
// to "pending" on the next scan so the owner can re-import it. Before the
// fix, upsertEntry's never-downgrade guard kept a removed series permanently
// stuck as "imported" with no way to re-import it.
func TestScan_DowngradesRemovedSeriesToPending(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)

	client := testdb.New(t)
	ctx := context.Background()
	svc := library.NewService(client, nil, nil, nil, func() {}, storage, sse.NewHub())

	// First scan stages the row as pending (no matching Series in the DB).
	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	path := found[0].Path
	if got := statusForPath(t, client, ctx, path); got != "pending" {
		t.Fatalf("first scan status = %q, want pending", got)
	}

	// Simulate a stale "imported" marking (e.g. the Series row was later
	// deleted by DeleteSeries, or import failed after marking imported).
	client.ImportEntry.Update().
		Where(importentry.Path(path)).
		SetStatus("imported").
		SaveX(ctx)

	// The Series row is STILL absent from the DB — a re-scan must downgrade
	// the entry back to pending, not preserve the stale "imported" status.
	if _, err := svc.Scan(ctx); err != nil {
		t.Fatalf("re-scan: %v", err)
	}
	if got := statusForPath(t, client, ctx, path); got != "pending" {
		t.Fatalf("after re-scan status = %q, want pending (downgraded — re-importable)", got)
	}
}

// TestScan_StaysImportedWhenSeriesStillPresent proves the companion half of
// the Slice R fix: a series whose Series row genuinely exists is recomputed as
// "imported" on every re-scan (not just left alone) — status always reflects
// live DB state, in both directions.
func TestScan_StaysImportedWhenSeriesStillPresent(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)

	client := testdb.New(t)
	ctx := context.Background()
	client.Series.Create().
		SetTitle("My Series").
		SetSlug(disk.Slugify("My Series")).
		SaveX(ctx)

	svc := library.NewService(client, nil, nil, nil, func() {}, storage, sse.NewHub())

	if _, err := svc.Scan(ctx); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	path := statusForPathByTitle(t, client, ctx)
	if got := statusForPath(t, client, ctx, path); got != "imported" {
		t.Fatalf("first scan status = %q, want imported", got)
	}

	// Re-scan with the Series STILL present must keep it imported.
	if _, err := svc.Scan(ctx); err != nil {
		t.Fatalf("re-scan: %v", err)
	}
	if got := statusForPath(t, client, ctx, path); got != "imported" {
		t.Fatalf("after re-scan status = %q, want imported (still present)", got)
	}
}

// statusForPathByTitle returns the single staged ImportEntry's path — a small
// helper for tests that don't already have the path in hand from Scan's
// return value.
func statusForPathByTitle(t *testing.T, client *ent.Client, ctx context.Context) string {
	t.Helper()
	e := client.ImportEntry.Query().OnlyX(ctx)
	return e.Path
}
