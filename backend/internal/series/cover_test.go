package series_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// seedCoverSeries seeds a categorized series with one provider carrying coverURL,
// AND creates its library folder — the cover is only cached for a series that has
// a folder on disk (SaveCover never creates one; see disk.ErrNoSeriesDir).
func seedCoverSeries(ctx context.Context, t *testing.T, db *ent.Client, storage, coverURL string) uuid.UUID {
	t.Helper()
	if err := os.MkdirAll(disk.SeriesDir(storage, "Manga", "Cover Cache"), 0o750); err != nil {
		t.Fatalf("mkdir series dir: %v", err)
	}
	return seedCoverSeriesNoDir(ctx, t, db, coverURL)
}

// seedCoverSeriesNoDir seeds the same series WITHOUT a folder on disk (a series
// that has downloaded nothing yet).
func seedCoverSeriesNoDir(ctx context.Context, t *testing.T, db *ent.Client, coverURL string) uuid.UUID {
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
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (cold): %v", err)
	}
	if string(cover.Data) != "IMG" || cover.Ext != "png" {
		t.Fatalf("CoverBytes (cold) = %q/%q, want IMG/png", cover.Data, cover.Ext)
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
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	// Warm the cache (the one and only permitted fetch).
	if _, err := svc.CoverBytes(ctx, id); err != nil {
		t.Fatalf("CoverBytes (warming): %v", err)
	}
	fetcher.calls.Store(0)

	for i := range 5 {
		cover, err := svc.CoverBytes(ctx, id)
		if err != nil {
			t.Fatalf("CoverBytes (warm, i=%d): %v", i, err)
		}
		if string(cover.Data) != "IMG" || cover.Ext != "jpg" {
			t.Fatalf("CoverBytes (warm) = %q/%q, want IMG/jpg", cover.Data, cover.Ext)
		}
	}

	if got := fetcher.calls.Load(); got != 0 {
		t.Fatalf("ANTI-BAN PROOF FAILED: warm serves made %d Suwayomi call(s), want 0", got)
	}
}

// TestCoverBytes_WarmServeNeverReadsTheSidecar is the SPEED proof. Once the DB
// fast-index (cover_file + cover_source_url) is populated, the warm path must
// read ONLY the image file — no tsundoku.json, no JSON parse. Deleting the
// sidecar (a file the hot path must not care about) leaves the serve working.
func TestCoverBytes_WarmServeNeverReadsTheSidecar(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("IMG"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	if _, err := svc.CoverBytes(ctx, id); err != nil {
		t.Fatalf("CoverBytes (warming): %v", err)
	}
	fetcher.calls.Store(0)

	// Destroy the sidecar. The index says which file to read, so this must not
	// matter at all — if it does, the hot path is still parsing tsundoku.json.
	seriesDir := disk.SeriesDir(storage, "Manga", "Cover Cache")
	if err := os.Remove(filepath.Join(seriesDir, "tsundoku.json")); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (warm, no sidecar): %v", err)
	}
	if string(cover.Data) != "IMG" || cover.Ext != "jpg" {
		t.Fatalf("CoverBytes (warm, no sidecar) = %q/%q, want IMG/jpg", cover.Data, cover.Ext)
	}
	if got := fetcher.calls.Load(); got != 0 {
		t.Fatalf("warm serve without a sidecar made %d Suwayomi call(s), want 0", got)
	}
}

// TestCoverBytes_ExistingCoverWithEmptyIndexBackfills is THE migration proof.
//
// The owner's library ALREADY has cover files + sidecar cover blocks on disk,
// while the new DB index columns are empty. Reading empty columns as "not
// cached" would re-fetch every one of those covers from the sources — precisely
// the hammering this cache exists to prevent. So: serve from the sidecar, make
// ZERO source calls, and backfill the index so the series self-heals onto the
// fast path.
func TestCoverBytes_ExistingCoverWithEmptyIndexBackfills(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("FETCHED"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	// Pre-existing on-disk cache, exactly as the pre-index feature left it:
	// the image + the sidecar cover block, and NOTHING in the DB.
	if _, err := disk.SaveCover(disk.CoverRequest{
		Storage:   storage,
		Category:  "Manga",
		Title:     "Cover Cache",
		Data:      []byte("ONDISK"),
		Ext:       "webp",
		SourceURL: "/thumb/a",
		Provider:  "src-a",
	}); err != nil {
		t.Fatalf("seed on-disk cover: %v", err)
	}
	if row := db.Series.GetX(ctx, id); row.CoverFile != "" || row.CoverSourceURL != "" {
		t.Fatalf("precondition: DB index must start empty, got %q/%q", row.CoverFile, row.CoverSourceURL)
	}

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (existing cover, empty index): %v", err)
	}
	if string(cover.Data) != "ONDISK" || cover.Ext != "webp" {
		t.Fatalf("CoverBytes = %q/%q, want ONDISK/webp (the file already on disk)", cover.Data, cover.Ext)
	}
	if got := fetcher.calls.Load(); got != 0 {
		t.Fatalf("MIGRATION PROOF FAILED: an already-cached cover made %d Suwayomi call(s), want 0", got)
	}

	row := db.Series.GetX(ctx, id)
	if row.CoverFile != "cover.webp" || row.CoverSourceURL != "/thumb/a" {
		t.Fatalf("index not backfilled: cover_file=%q cover_source_url=%q, want cover.webp//thumb/a",
			row.CoverFile, row.CoverSourceURL)
	}
}

// TestCoverBytes_IndexedFileVanishedRefetches proves the index is advisory: if
// the file it names is gone (an owner deleted it, an NFS blip), the cover is
// re-fetched once rather than 404ing.
func TestCoverBytes_IndexedFileVanishedRefetches(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("IMG"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	if _, err := svc.CoverBytes(ctx, id); err != nil {
		t.Fatalf("CoverBytes (warming): %v", err)
	}
	seriesDir := disk.SeriesDir(storage, "Manga", "Cover Cache")
	if err := os.Remove(filepath.Join(seriesDir, "cover.jpg")); err != nil {
		t.Fatalf("remove cover file: %v", err)
	}
	fetcher.calls.Store(0)

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (vanished file): %v", err)
	}
	if string(cover.Data) != "IMG" {
		t.Errorf("CoverBytes (vanished file) = %q, want IMG", cover.Data)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("vanished cover: Suwayomi calls = %d, want exactly 1", got)
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
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	if _, err := svc.CoverBytes(ctx, id); err != nil {
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

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (invalidated): %v", err)
	}
	if string(cover.Data) != "NEW" || cover.Ext != "png" {
		t.Fatalf("CoverBytes (invalidated) = %q/%q, want NEW/png", cover.Data, cover.Ext)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("invalidation: Suwayomi calls = %d, want exactly 1", got)
	}

	// And the new cover is now the cache: zero further calls.
	fetcher.calls.Store(0)
	if _, err := svc.CoverBytes(ctx, id); err != nil {
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
	storage := t.TempDir()

	fetcher := &countingFetcher{data: []byte("IMG"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	// The series folder exists but is not writable ⇒ the cover write fails.
	seriesDir := disk.SeriesDir(storage, "Manga", "Cover Cache")
	// G302: a read-only DIRECTORY (r-x) is exactly what makes the cache write fail;
	// dir modes legitimately need the exec bit, and this is test-only.
	if err := os.Chmod(seriesDir, 0o500); err != nil { //nolint:gosec
		t.Fatalf("chmod series dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(seriesDir, 0o750) }) //nolint:gosec

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (disk write failure): want the fetched bytes, got error %v", err)
	}
	if string(cover.Data) != "IMG" || cover.Ext != "jpg" {
		t.Errorf("CoverBytes (disk write failure) = %q/%q, want IMG/jpg", cover.Data, cover.Ext)
	}
}

// TestCoverBytes_NoSeriesDirStillServesAndCreatesNothing proves a series with
// nothing downloaded (no folder) is served live and NEVER has a folder
// materialised for it — otherwise rendering the grid would litter the library
// with cover-only dirs the import wizard then stages as ghosts.
func TestCoverBytes_NoSeriesDirStillServesAndCreatesNothing(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("IMG"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeriesNoDir(ctx, t, db, "/thumb/a")

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (no series dir): %v", err)
	}
	if string(cover.Data) != "IMG" {
		t.Errorf("CoverBytes (no series dir) = %q, want IMG", cover.Data)
	}

	entries, err := os.ReadDir(storage)
	if err != nil {
		t.Fatalf("read storage: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("serving the cover created %d entry/entries in the library, want 0", len(entries))
	}
}

// TestCoverBytes_ExtNormalisedColdAndWarm proves the cold response and the warm
// (disk) response agree on the extension when the source reports an uppercase
// type — a cold "JPEG" must not serve as octet-stream while the warm one is jpeg.
func TestCoverBytes_ExtNormalisedColdAndWarm(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("IMG"), ext: "JPEG"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	cold, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (cold): %v", err)
	}
	warm, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (warm): %v", err)
	}
	if cold.Ext != "jpeg" || warm.Ext != "jpeg" {
		t.Errorf("ext cold/warm = %q/%q, want jpeg/jpeg", cold.Ext, warm.Ext)
	}
}

// TestCoverBytes_FetchFailure proves a cold-cover Suwayomi failure surfaces as
// ErrCoverFetchFailed (→ 502), never a false success.
func TestCoverBytes_FetchFailure(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fetcher := &countingFetcher{err: errors.New("suwayomi down")}
	svc := series.NewService(db, t.TempDir(), 14).WithCoverFetcher(fetcher)
	id := seedCoverSeriesNoDir(ctx, t, db, "/thumb/a")

	if _, err := svc.CoverBytes(ctx, id); !errors.Is(err, series.ErrCoverFetchFailed) {
		t.Fatalf("CoverBytes: err = %v, want ErrCoverFetchFailed", err)
	}
}

// TestCoverBytes_NoCoverAndUnknownSeries proves the two 404 paths are unchanged.
func TestCoverBytes_NoCoverAndUnknownSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14).WithCoverFetcher(&countingFetcher{})

	noCover := seedCoverSeriesNoDir(ctx, t, db, "")
	if _, err := svc.CoverBytes(ctx, noCover); !errors.Is(err, series.ErrNoCover) {
		t.Errorf("CoverBytes (no cover_url): err = %v, want ErrNoCover", err)
	}
	if _, err := svc.CoverBytes(ctx, uuid.New()); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Errorf("CoverBytes (unknown id): err = %v, want ErrSeriesNotFound", err)
	}
}

// TestCoverBytes_NoFetcherConfigured proves a service built without a cover
// fetcher fails loudly on a cold cover instead of nil-panicking.
func TestCoverBytes_NoFetcherConfigured(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	id := seedCoverSeriesNoDir(ctx, t, db, "/thumb/a")

	if _, err := svc.CoverBytes(ctx, id); !errors.Is(err, series.ErrCoverFetchFailed) {
		t.Fatalf("CoverBytes (no fetcher): err = %v, want ErrCoverFetchFailed", err)
	}
}

// TestCoverVersion_TracksBytesNotSourceURL is THE one-way-door proof.
//
// SeriesProvider.cover_url is Suwayomi's id-derived thumbnail path
// (/api/v1/manga/{id}/thumbnail) — it is STABLE even when the source republishes
// different art. So a version derived from that URL would never change, while the
// served bytes did, and the immutable Cache-Control would pin the OLD image in
// the browser for a YEAR with no lever to fix it.
//
// Here the cover_url NEVER changes; only the bytes do (the local file is lost —
// an NFS blip — and the re-fetch brings the source's new art). The version MUST
// change, because the version is a hash of the bytes.
func TestCoverVersion_TracksBytesNotSourceURL(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("OLD-ART"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/api/v1/manga/7/thumbnail")

	first, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (cold): %v", err)
	}
	if first.Version == "" {
		t.Fatal("a cached cover must carry a content version")
	}

	// The source republishes different art under the SAME (id-derived) cover_url,
	// and the local file is gone, so the next serve re-fetches it.
	if err := os.Remove(filepath.Join(disk.SeriesDir(storage, "Manga", "Cover Cache"), "cover.jpg")); err != nil {
		t.Fatalf("remove cover file: %v", err)
	}
	fetcher.data = []byte("NEW-ART")

	second, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (new art): %v", err)
	}
	if string(second.Data) != "NEW-ART" {
		t.Fatalf("CoverBytes (new art) = %q, want NEW-ART", second.Data)
	}
	if second.Version == first.Version {
		t.Fatalf("IMMUTABLE PROOF FAILED: the cover bytes changed but the version did not (%q) — "+
			"an immutable URL would pin the old image forever", second.Version)
	}

	// The DTO's URL must carry the NEW version, or the browser never re-fetches.
	detail, err := svc.GetSeries(ctx, id)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if !strings.HasSuffix(detail.CoverURL, "?v="+second.Version) {
		t.Errorf("coverUrl = %q, want it to carry the new version %q", detail.CoverURL, second.Version)
	}
}

// TestCoverVersion_ReindexesAnOutOfBandFileEdit proves the version can never lie
// about what is on disk: the owner drops their OWN cover.jpg into the series
// folder (no fetch, no save), and the next serve re-derives the version from the
// bytes it actually read and re-indexes. Without this, immutable would keep the
// browser on the previous image forever.
func TestCoverVersion_ReindexesAnOutOfBandFileEdit(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fetcher := &countingFetcher{data: []byte("FETCHED"), ext: "jpg"}
	svc := series.NewService(db, storage, 14).WithCoverFetcher(fetcher)
	id := seedCoverSeries(ctx, t, db, storage, "/thumb/a")

	first, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (cold): %v", err)
	}

	// The owner overwrites the cached file with their own artwork.
	coverPath := filepath.Join(disk.SeriesDir(storage, "Manga", "Cover Cache"), "cover.jpg")
	if err := os.WriteFile(coverPath, []byte("OWNER-ART"), 0o600); err != nil {
		t.Fatalf("overwrite cover file: %v", err)
	}
	fetcher.calls.Store(0)

	second, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (owner art): %v", err)
	}
	if string(second.Data) != "OWNER-ART" {
		t.Fatalf("CoverBytes = %q, want the owner's file OWNER-ART", second.Data)
	}
	if second.Version == first.Version {
		t.Fatal("an out-of-band file edit left the version unchanged — immutable would pin the old image")
	}
	if got := fetcher.calls.Load(); got != 0 {
		t.Errorf("serving the owner's own file made %d Suwayomi call(s), want 0", got)
	}

	stored, err := svc.CoverVersion(ctx, id)
	if err != nil {
		t.Fatalf("CoverVersion: %v", err)
	}
	if stored != second.Version {
		t.Errorf("CoverVersion = %q, want the re-indexed %q", stored, second.Version)
	}
}

// TestCoverVersion_EmptyWhenNothingCached proves an uncached (live-proxied) cover
// carries NO version — so its URL is unversioned and the endpoint can never mark
// it immutable. Unknown series is still ErrSeriesNotFound.
func TestCoverVersion_EmptyWhenNothingCached(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14).WithCoverFetcher(&countingFetcher{data: []byte("IMG"), ext: "jpg"})
	id := seedCoverSeriesNoDir(ctx, t, db, "/thumb/a")

	cover, err := svc.CoverBytes(ctx, id)
	if err != nil {
		t.Fatalf("CoverBytes (no series dir): %v", err)
	}
	if cover.Version != "" {
		t.Errorf("an uncached cover carries version %q, want \"\" (nothing durable backs an immutable promise)", cover.Version)
	}

	version, err := svc.CoverVersion(ctx, id)
	if err != nil {
		t.Fatalf("CoverVersion: %v", err)
	}
	if version != "" {
		t.Errorf("CoverVersion = %q, want \"\"", version)
	}

	detail, err := svc.GetSeries(ctx, id)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if strings.Contains(detail.CoverURL, "?v=") {
		t.Errorf("coverUrl = %q, want no ?v= for an uncached cover", detail.CoverURL)
	}

	if _, err := svc.CoverVersion(ctx, uuid.New()); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Errorf("CoverVersion (unknown id): err = %v, want ErrSeriesNotFound", err)
	}
}
