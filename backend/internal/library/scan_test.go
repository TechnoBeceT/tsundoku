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
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/library"
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
	svc := library.NewService(client, nil, nil, nil, func() {}, storage)
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
