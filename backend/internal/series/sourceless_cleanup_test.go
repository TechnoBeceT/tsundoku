package series_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// mustUUID parses a string id produced by the service layer (e.g. ch.ID.String())
// back into a uuid.UUID, failing the test on a malformed id rather than the
// service — a parse failure here means the test seeded the wrong thing, not that
// the code under test is broken.
func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("mustUUID(%q): %v", s, err)
	}
	return id
}

// itoa is a tiny int-to-string helper so the sourceless fixtures don't need a
// strconv import just to build chapter keys.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// errorsIs is errors.Is spelled as a one-liner so assertions read as a single
// call at the call site.
func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}

// writeFakeCBZ writes a stub CBZ at <storage>/<category>/<title>/<filename> — the
// same disk.SeriesDir layout the cleanup deletion path resolves. Reuses the
// existing writeCBZ helper (dedupe_files_test.go) rather than duplicating the
// directory-creation logic.
func writeFakeCBZ(t *testing.T, storage, category, title, filename string) {
	t.Helper()
	writeCBZ(t, filepath.Join(storage, category, title), filename)
}

// seedSourceless builds a series with providers carrying keys 1..carried and
// downloaded chapters 1..downloaded; chapters beyond `carried` are sourceless.
// Returns the series id and a map chapter_key -> chapter id.
func seedSourceless(t *testing.T, ctx context.Context, db *ent.Client) (string, map[string]string) {
	t.Helper()
	s := db.Series.Create().SetTitle("Sourceless").SetSlug("sourceless").SetMonitored(true).SaveX(ctx)
	// One provider carrying keys "1".."66".
	sp := db.SeriesProvider.Create().SetSeries(s).SetProvider("src-a").SetImportance(50).SaveX(ctx)
	ids := map[string]string{}
	for n := 1; n <= 73; n++ {
		key := itoa(n)
		ch := db.Chapter.Create().
			SetSeries(s).SetChapterKey(key).SetNumber(float64(n)).
			SetState(entchapter.StateDownloaded).
			SetFilename("[src] Sourceless " + key + ".cbz").
			SaveX(ctx)
		ids[key] = ch.ID.String()
		if n <= 66 {
			db.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey(key).SaveX(ctx)
		}
	}
	return s.ID.String(), ids
}

func TestSourcelessCleanupPreview_OffersOnlyZeroCarrierDownloaded(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sid, _ := seedSourceless(t, ctx, db)

	svc := series.NewService(db, t.TempDir(), 7)
	out, err := svc.SourcelessCleanupPreview(ctx, mustUUID(t, sid))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	// Keys 67..73 (7 chapters) are sourceless; 1..66 are carried.
	if len(out.Chapters) != 7 {
		t.Fatalf("preview chapters = %d, want 7 (keys 67..73)", len(out.Chapters))
	}
	for _, c := range out.Chapters {
		if c.Number != nil && *c.Number <= 66 {
			t.Errorf("carried chapter %v offered as sourceless", *c.Number)
		}
	}
}

func TestRemoveSourcelessChapters_RemovesRowAndRejectsCarried(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	sid, ids := seedSourceless(t, ctx, db)
	// Create the CBZ on disk for key "70" so removeCleanupFiles finds a file to delete.
	writeFakeCBZ(t, storage, "", "Sourceless", "[src] Sourceless 70.cbz")

	svc := series.NewService(db, storage, 7)

	// A carried chapter (key "10") must be rejected → nothing deleted.
	if _, err := svc.RemoveSourcelessChapters(ctx, mustUUID(t, sid), []uuid.UUID{mustUUID(t, ids["10"])}); !errorsIs(err, series.ErrChapterNotRemovable) {
		t.Fatalf("carried chapter accepted, err = %v, want ErrChapterNotRemovable", err)
	}

	// A sourceless chapter (key "70") is removed.
	n, err := svc.RemoveSourcelessChapters(ctx, mustUUID(t, sid), []uuid.UUID{mustUUID(t, ids["70"])})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed = %d, want 1", n)
	}
	if exists, _ := db.Chapter.Query().Where(entchapter.ID(mustUUID(t, ids["70"]))).Exist(ctx); exists {
		t.Error("sourceless chapter row 70 still present after removal")
	}
	// The carried chapter 10 is untouched.
	if exists, _ := db.Chapter.Query().Where(entchapter.ID(mustUUID(t, ids["10"]))).Exist(ctx); !exists {
		t.Error("carried chapter 10 was deleted, want kept")
	}
}
