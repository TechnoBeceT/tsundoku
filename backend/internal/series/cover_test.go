package series_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

// countingFetcher is a series.CoverFetcher that counts every fetch. The whole
// point of the local cover cache is that this counter STOPS going up.
type countingFetcher struct {
	calls atomic.Int32
	data  []byte
	ext   string
	err   error
}

func (f *countingFetcher) PageBytes(context.Context, string) ([]byte, string, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, "", f.err
	}
	return f.data, f.ext, nil
}

// seedCoverSeries seeds a categorized series with one provider carrying coverURL.
func seedCoverSeries(ctx context.Context, t *testing.T, db *ent.Client, coverURL string) uuid.UUID {
	t.Helper()
	catID, err := category.IDByName(ctx, db, "Manga")
	if err != nil {
		t.Fatalf("IDByName: %v", err)
	}
	s := db.Series.Create().
		SetTitle("Cover Cache").
		SetSlug("cover-cache").
		SetCategoryID(catID).
		SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("src-a").
		SetImportance(10).
		SetCoverURL(coverURL).
		SaveX(ctx)
	return s.ID
}

// TestCoverBytes_ColdFetchesOnceAndCaches proves a first view fetches exactly
// once from Suwayomi and writes both the image and the sidecar cover block.
func TestCoverBytes_ColdFetchesOnceAndCaches(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("IMG"), ext: "png"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, "/thumb/a")

	data, ext, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (cold): %v", err)
	}
	if string(data) != "IMG" || ext != "png" {
		t.Fatalf("CoverBytes (cold) = %q/%q, want IMG/png", data, ext)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("cold fetch: Suwayomi calls = %d, want 1", got)
	}

	seriesDir := disk.SeriesDir(storage, "Manga", "Cover Cache")
	if _, err := os.Stat(filepath.Join(seriesDir, "cover.png")); err != nil {
		t.Fatalf("cover file not written: %v", err)
	}
	sc, err := disk.ReadSidecar(seriesDir)
	if err != nil || sc == nil || sc.Cover == nil {
		t.Fatalf("sidecar cover block not written (err %v, sidecar %v)", err, sc)
	}
	if sc.Cover.SourceURL != "/thumb/a" {
		t.Errorf("sidecar source_url = %q, want /thumb/a", sc.Cover.SourceURL)
	}
}

// TestCoverBytes_WarmMakesZeroSuwayomiCalls is THE anti-ban proof: once the
// cover is on disk and the sidecar's source_url still matches the metadata
// provider's cover_url, serving it must never touch Suwayomi again. 52 series
// rendered repeatedly = 0 source-ward pings.
func TestCoverBytes_WarmMakesZeroSuwayomiCalls(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("IMG"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, "/thumb/a")

	// Warm the cache (the one and only permitted fetch).
	if _, _, err := svc.CoverBytes(ctx, id); err != nil {
		t.Fatalf("CoverBytes (warming): %v", err)
	}
	fetcher.calls.Store(0)

	for i := range 5 {
		data, ext, err := svc.CoverBytes(ctx, id)
		if err != nil {
			t.Fatalf("CoverBytes (warm, i=%d): %v", i, err)
		}
		if string(data) != "IMG" || ext != "jpg" {
			t.Fatalf("CoverBytes (warm) = %q/%q, want IMG/jpg", data, ext)
		}
	}

	if got := fetcher.calls.Load(); got != 0 {
		t.Fatalf("ANTI-BAN PROOF FAILED: warm serves made %d Suwayomi call(s), want 0", got)
	}
}

// TestCoverBytes_SourceURLChangeRefetchesOnce proves the single invalidation
// rule: a metadata-source switch (a different cover_url) re-fetches EXACTLY
// once, overwrites the file, and then goes quiet again.
func TestCoverBytes_SourceURLChangeRefetchesOnce(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("OLD"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, "/thumb/a")

	if _, _, err := svc.CoverBytes(ctx, id); err != nil {
		t.Fatalf("CoverBytes (warming): %v", err)
	}

	// A second, higher-importance provider takes over the metadata source: a new
	// cover_url ⇒ the sidecar's source_url no longer matches.
	db.SeriesProvider.Create().
		SetSeriesID(id).
		SetProvider("src-b").
		SetImportance(20).
		SetCoverURL("/thumb/b").
		SaveX(ctx)
	fetcher.data, fetcher.ext = []byte("NEW"), "png"
	fetcher.calls.Store(0)

	data, ext, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (invalidated): %v", err)
	}
	if string(data) != "NEW" || ext != "png" {
		t.Fatalf("CoverBytes (invalidated) = %q/%q, want NEW/png", data, ext)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("invalidation: Suwayomi calls = %d, want exactly 1", got)
	}

	// And the new cover is now the cache: zero further calls.
	fetcher.calls.Store(0)
	if _, _, err := svc.CoverBytes(ctx, id); err != nil {
		t.Fatalf("CoverBytes (re-warmed): %v", err)
	}
	if got := fetcher.calls.Load(); got != 0 {
		t.Fatalf("re-warmed: Suwayomi calls = %d, want 0", got)
	}
}

// TestCoverBytes_DiskWriteFailureStillServes proves a cache that cannot persist
// does not break the page: the fetched bytes are still returned.
func TestCoverBytes_DiskWriteFailureStillServes(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	// A storage root that is a FILE, not a directory ⇒ every disk write fails.
	storage := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(storage, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	fetcher := &countingFetcher{data: []byte("IMG"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, "/thumb/a")

	data, ext, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (disk write failure): want the fetched bytes, got error %v", err)
	}
	if string(data) != "IMG" || ext != "jpg" {
		t.Errorf("CoverBytes (disk write failure) = %q/%q, want IMG/jpg", data, ext)
	}
}

// TestCoverBytes_FetchFailure proves a cold-cover Suwayomi failure surfaces as
// ErrCoverFetchFailed (→ 502), never a false success.
func TestCoverBytes_FetchFailure(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fetcher := &countingFetcher{err: errors.New("suwayomi down")}
	svc := series.NewService(db, t.TempDir(), 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, "/thumb/a")

	if _, _, err := svc.CoverBytes(ctx, id); !errors.Is(err, series.ErrCoverFetchFailed) {
		t.Fatalf("CoverBytes: err = %v, want ErrCoverFetchFailed", err)
	}
}

// TestCoverBytes_NoCoverAndUnknownSeries proves the two 404 paths are unchanged.
func TestCoverBytes_NoCoverAndUnknownSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14).WithCoverFetcher(&countingFetcher{})

	noCover := seedCoverSeries(ctx, t, db, "")
	if _, _, err := svc.CoverBytes(ctx, noCover); !errors.Is(err, series.ErrNoCover) {
		t.Errorf("CoverBytes (no cover_url): err = %v, want ErrNoCover", err)
	}
	if _, _, err := svc.CoverBytes(ctx, uuid.New()); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Errorf("CoverBytes (unknown id): err = %v, want ErrSeriesNotFound", err)
	}
}

// TestCoverBytes_NoFetcherConfigured proves a service built without a cover
// fetcher fails loudly on a cold cover instead of nil-panicking.
func TestCoverBytes_NoFetcherConfigured(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	id := seedCoverSeries(ctx, t, db, "/thumb/a")

	if _, _, err := svc.CoverBytes(ctx, id); !errors.Is(err, series.ErrCoverFetchFailed) {
		t.Fatalf("CoverBytes (no fetcher): err = %v, want ErrCoverFetchFailed", err)
	}
}
