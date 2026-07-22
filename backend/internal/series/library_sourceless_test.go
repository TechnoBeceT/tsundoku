package series_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

func TestLibrarySourceless_ListsSeriesWithCount(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sid, _ := seedSourceless(t, ctx, db) // 7 sourceless (keys 67..73)

	svc := series.NewService(db, t.TempDir(), 7)
	out, err := svc.LibrarySourceless(ctx)
	if err != nil {
		t.Fatalf("LibrarySourceless: %v", err)
	}
	if len(out.Series) != 1 {
		t.Fatalf("series listed = %d, want 1", len(out.Series))
	}
	if out.Series[0].SeriesID != sid || out.Series[0].SourcelessCount != 7 {
		t.Errorf("row = {%s, %d}, want {%s, 7}", out.Series[0].SeriesID, out.Series[0].SourcelessCount, sid)
	}
}

// seedManySourcelessSeries creates n series, each with exactly one DOWNLOADED
// chapter carried by no provider (zero carriers by construction) — the worst
// case for an N+1 (every row needs its own removableSourceless resolution).
func seedManySourcelessSeries(ctx context.Context, t *testing.T, client *ent.Client, prefix string, n int) {
	t.Helper()
	for i := range n {
		s := client.Series.Create().
			SetTitle(fmt.Sprintf("%s Series %02d", prefix, i)).
			SetSlug(fmt.Sprintf("%s-series-%02d", prefix, i)).
			SetMonitored(true).
			SaveX(ctx)
		client.Chapter.Create().SetSeriesID(s.ID).
			SetChapterKey(fmt.Sprintf("%s-%d", prefix, i)).
			SetState(entchapter.StateDownloaded).
			SetFilename(fmt.Sprintf("[src] %s Series %02d 1.cbz", prefix, i)).
			SaveX(ctx)
	}
}

// TestLibrarySourcelessQueryCountIsSeriesCountIndependent is the NO-N+1 proof:
// LibrarySourceless resolves every row IN MEMORY from one bounded eager load, so
// the SQL read count must be identical for a 2-series and a 20-series library.
func TestLibrarySourcelessQueryCountIsSeriesCountIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	storage := t.TempDir()

	client, drv := newCountingClient(db)
	count := func(want int) int64 {
		svc := series.NewService(client, storage, 14)
		drv.queries.Store(0)
		dto, err := svc.LibrarySourceless(ctx)
		if err != nil {
			t.Fatalf("LibrarySourceless: %v", err)
		}
		if len(dto.Series) != want {
			t.Fatalf("listed %d series, want %d", len(dto.Series), want)
		}
		return drv.queries.Load()
	}

	seedManySourcelessSeries(ctx, t, seedClient, "aaa", 2)
	smallQ := count(2)
	seedManySourcelessSeries(ctx, t, seedClient, "bbb", 18)
	bigQ := count(20)

	if smallQ != bigQ {
		t.Errorf("N+1: %d queries for 2 series but %d for 20 — it must be flat", smallQ, bigQ)
	}
	const maxQueries = 6
	if bigQ > maxQueries {
		t.Errorf("LibrarySourceless issued %d queries, want <= %d (one bounded load + its eager loads)", bigQ, maxQueries)
	}
	t.Logf("queries: 2 series=%d 20 series=%d", smallQ, bigQ)
}

func TestLibrarySourceless_EmptyIsSlice(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 7)
	out, err := svc.LibrarySourceless(ctx)
	if err != nil {
		t.Fatalf("LibrarySourceless: %v", err)
	}
	if out.Series == nil {
		t.Error("Series is nil, want non-nil empty slice (renders [] not null)")
	}
}
