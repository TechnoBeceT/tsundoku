package download_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// redownloadFixture is one downloaded chapter with a real CBZ on disk, ready to be
// put back through the engine.
type redownloadFixture struct {
	client    *ent.Client
	storage   string
	chapterID uuid.UUID
	// cbzPath is the absolute path of the rendered CBZ.
	cbzPath string
	// seriesDir is the directory the CBZ lives in, so a test can count files in it.
	seriesDir string
}

// newDownloadedChapter seeds a series with one source and drives a REAL download
// cycle so the chapter ends up downloaded with an actual CBZ on disk. Everything
// after this point exercises the re-download over a file that genuinely exists.
func newDownloadedChapter(ctx context.Context, t *testing.T, f fetcher.ChapterFetcher) redownloadFixture {
	t.Helper()

	client := testdb.New(t)
	storage := mustTempDir(t)

	s := client.Series.Create().SetTitle("Redownload Series").SetSlug("redownload-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("comix").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("ch-1").
		SetURL("https://comix.example/ch1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)

	d := download.New(client, f, sse.NewHub(), download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("seed download: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("seed download left state %s; want downloaded", got.State)
	}
	seriesDir := filepath.Join(storage, "Other", "Redownload Series")
	cbzPath := filepath.Join(seriesDir, got.Filename)
	if _, err := os.Stat(cbzPath); err != nil {
		t.Fatalf("seed CBZ missing at %s: %v", cbzPath, err)
	}
	return redownloadFixture{client: client, storage: storage, chapterID: ch.ID, cbzPath: cbzPath, seriesDir: seriesDir}
}

// cbzFileCount counts the CBZ files in a series directory, so a test can prove a
// re-download left no orphan behind.
func cbzFileCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read series dir %s: %v", dir, err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".cbz" {
			n++
		}
	}
	return n
}

// TestRedownload_FailedAttemptKeepsTheExistingCBZ is the owner-ratified guarantee
// of QCAT-343, proved end to end: a re-download marks the chapter wanted WITHOUT
// deleting anything, so when the fresh fetch FAILS the previously-downloaded CBZ is
// still on disk, byte-for-byte. The rejected alternative (delete on trigger) would
// have left nothing here.
//
// This is also why re-download adds NO new Rule 2 deletion path.
func TestRedownload_FailedAttemptKeepsTheExistingCBZ(t *testing.T) {
	ctx := context.Background()
	fx := newDownloadedChapter(ctx, t, fake.New())

	before, err := os.ReadFile(fx.cbzPath)
	if err != nil {
		t.Fatalf("read seeded CBZ: %v", err)
	}

	if err := downloads.NewService(fx.client).RedownloadChapter(ctx, fx.chapterID); err != nil {
		t.Fatalf("RedownloadChapter: %v", err)
	}
	// The trigger itself must not touch the file.
	if _, statErr := os.Stat(fx.cbzPath); statErr != nil {
		t.Fatalf("CBZ gone immediately after the re-download trigger: %v", statErr)
	}

	// Now let the re-download attempt FAIL against every source.
	failing := download.New(fx.client, fake.New(fake.WithError(errors.New("comix: connection reset"))),
		sse.NewHub(), download.Config{Storage: fx.storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := failing.RunOnce(ctx); err != nil {
		t.Fatalf("failing RunOnce: %v", err)
	}

	got := fx.client.Chapter.GetX(ctx, fx.chapterID)
	if got.State == entchapter.StateDownloaded {
		t.Fatalf("fixture: the re-download was supposed to fail, but the chapter is downloaded again")
	}

	after, err := os.ReadFile(fx.cbzPath)
	if err != nil {
		t.Fatalf("the existing CBZ did NOT survive a failed re-download: %v", err)
	}
	if string(after) != string(before) {
		t.Error("the existing CBZ was modified by a failed re-download; it must be left untouched")
	}
}

// TestRedownload_SuccessOverwritesInPlace proves the other half of QCAT-343: the
// same source + the same chapter renders the same filename, so a successful
// re-download overwrites the file in place and creates NO orphan CBZ to clean up.
func TestRedownload_SuccessOverwritesInPlace(t *testing.T) {
	ctx := context.Background()
	fx := newDownloadedChapter(ctx, t, fake.New(fake.WithPages(4)))

	if got := cbzFileCount(t, fx.seriesDir); got != 1 {
		t.Fatalf("fixture: %d CBZ files before the re-download; want 1", got)
	}
	nameBefore := fx.client.Chapter.GetX(ctx, fx.chapterID).Filename

	if err := downloads.NewService(fx.client).RedownloadChapter(ctx, fx.chapterID); err != nil {
		t.Fatalf("RedownloadChapter: %v", err)
	}

	// A fresh render with a DIFFERENT page count, so the rewrite is observable.
	d := download.New(fx.client, fake.New(fake.WithPages(9)), sse.NewHub(),
		download.Config{Storage: fx.storage}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("re-download RunOnce: %v", err)
	}

	got := fx.client.Chapter.GetX(ctx, fx.chapterID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("state = %s; want downloaded", got.State)
	}
	if got.Filename != nameBefore {
		t.Errorf("filename = %q; want the same %q (same source + chapter ⇒ same filename)", got.Filename, nameBefore)
	}
	if got.PageCount == nil || *got.PageCount != 9 {
		t.Errorf("page count = %v; want 9 (the file was really re-rendered)", got.PageCount)
	}
	if n := cbzFileCount(t, fx.seriesDir); n != 1 {
		t.Errorf("%d CBZ files after the re-download; want 1 (overwrite in place, no orphan)", n)
	}
}
