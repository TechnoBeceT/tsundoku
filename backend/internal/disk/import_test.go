package disk_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// writeKaizokuCBZ writes <storage>/<category>/<title>/<filename> as a CBZ whose
// ONLY provenance is a Kaizoku-style ComicInfo (Publisher/Translator, no
// Tsundoku extensions) plus the Kaizoku filename bracket.
func writeKaizokuCBZ(t *testing.T, storage, category, title, filename, number, publisher, translator string) {
	t.Helper()
	dir := filepath.Join(storage, category, title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	page, _ := zw.Create("001.jpg")
	_, _ = page.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9}) // minimal jpeg-ish bytes
	ciw, _ := zw.Create("ComicInfo.xml")
	_, _ = ciw.Write([]byte(`<?xml version="1.0"?><ComicInfo><Series>` + title +
		`</Series><Number>` + number + `</Number><Publisher>` + publisher +
		`</Publisher><Translator>` + translator + `</Translator></ComicInfo>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileOne_ImportsKaizokuSeriesAsDownloaded(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuCBZ(t, storage, "Manga", "My Series",
		"[mangadex-Alpha][en] My Series 1.cbz", "1", "mangadex", "Alpha")
	writeKaizokuCBZ(t, storage, "Manga", "My Series",
		"[mangadex-Alpha][en] My Series 2.cbz", "2", "mangadex", "Alpha")

	client := testdb.New(t)
	ctx := context.Background()

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(facts) != 1 || facts[0].Title != "My Series" {
		t.Fatalf("unexpected facts: %+v", facts)
	}

	if _, err := disk.ReconcileOne(ctx, client, facts[0]); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Provider row exists with importance 1, provider name from the adapter.
	sp, err := client.SeriesProvider.Query().
		Where(seriesprovider.Provider("mangadex")).Only(ctx)
	if err != nil {
		t.Fatalf("provider query: %v", err)
	}
	if sp.Importance != 1 {
		t.Fatalf("importance = %d, want 1", sp.Importance)
	}

	// Both chapters registered as downloaded; NONE wanted (no re-download).
	downloaded, _ := client.Chapter.Query().Where(chapter.StateEQ(chapter.StateDownloaded)).Count(ctx)
	wanted, _ := client.Chapter.Query().Where(chapter.StateEQ(chapter.StateWanted)).Count(ctx)
	if downloaded != 2 || wanted != 0 {
		t.Fatalf("downloaded=%d wanted=%d, want 2/0", downloaded, wanted)
	}
}
