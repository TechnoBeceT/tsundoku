package series_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestBackfillFirstDownloadedAt proves the three cases the one-time backfill
// must handle, then proves it is idempotent (a second run changes nothing).
func TestBackfillFirstDownloadedAt(t *testing.T) {
	ctx := context.Background()
	client, db := testdb.NewWithSQL(t)

	s := client.Series.Create().SetTitle("Backfill Series").SetSlug("backfill-series").SaveX(ctx)

	downloadDateA := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	chapterA := client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("1").
		SetDownloadDate(downloadDateA).
		SaveX(ctx)

	downloadDateB := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)
	existingFirstDownloadedB := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	chapterB := client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("2").
		SetDownloadDate(downloadDateB).
		SetFirstDownloadedAt(existingFirstDownloadedB).
		SaveX(ctx)

	chapterC := client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("3").
		SaveX(ctx)

	backfillX(ctx, t, db, 1)

	assertFirstDownloadedAt(ctx, t, client, chapterA.ID, &downloadDateA)
	assertFirstDownloadedAt(ctx, t, client, chapterB.ID, &existingFirstDownloadedB)
	assertFirstDownloadedAt(ctx, t, client, chapterC.ID, nil)

	// Idempotent: a second run touches nothing (chapter A already backfilled,
	// B was never NULL, C has no download_date to copy from).
	backfillX(ctx, t, db, 0)

	assertFirstDownloadedAt(ctx, t, client, chapterA.ID, &downloadDateA)

	// Sanity: confirm via the typed predicate too, mirroring the write-once
	// guard the field's own doc comment describes.
	stillNil, err := client.Chapter.Query().
		Where(entchapter.ID(chapterC.ID), entchapter.FirstDownloadedAtIsNil()).
		Exist(ctx)
	if err != nil {
		t.Fatalf("query chapter C: %v", err)
	}
	if !stillNil {
		t.Fatal("chapter C: expected FirstDownloadedAtIsNil to still hold")
	}
}

// backfillX runs series.BackfillFirstDownloadedAt and asserts the rows-affected
// count matches wantRows, failing the test on either an error or a mismatch.
func backfillX(ctx context.Context, t *testing.T, db *sql.DB, wantRows int64) {
	t.Helper()
	rows, err := series.BackfillFirstDownloadedAt(ctx, db)
	if err != nil {
		t.Fatalf("BackfillFirstDownloadedAt: %v", err)
	}
	if rows != wantRows {
		t.Fatalf("BackfillFirstDownloadedAt: rows affected = %d, want %d", rows, wantRows)
	}
}

// assertFirstDownloadedAt loads the chapter and asserts its FirstDownloadedAt
// matches want (nil means "still NULL — no evidence of arrival").
func assertFirstDownloadedAt(ctx context.Context, t *testing.T, client *ent.Client, chapterID uuid.UUID, want *time.Time) {
	t.Helper()
	got := client.Chapter.GetX(ctx, chapterID)
	switch {
	case want == nil:
		if got.FirstDownloadedAt != nil {
			t.Fatalf("chapter %s FirstDownloadedAt = %v, want nil (no evidence of arrival)", chapterID, got.FirstDownloadedAt)
		}
	case got.FirstDownloadedAt == nil || !got.FirstDownloadedAt.Equal(*want):
		t.Fatalf("chapter %s FirstDownloadedAt = %v, want %v", chapterID, got.FirstDownloadedAt, *want)
	}
}
