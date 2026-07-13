package metadatasvc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
)

// coverServer returns an httptest.Server that always answers one fixed PNG
// body — a stand-in for a metadata provider's cover host.
func coverServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSetCover_FetchesCachesAndSetsCoverSource is the owner-picked-cover
// round-trip: SetCover fetches the given URL's bytes, caches them on disk via
// the Local Cover Cache, and records cover_source independently of any
// metadata_source (QCAT-228: cover selection is never coupled to the
// rich-metadata merge).
func TestSetCover_FetchesCachesAndSetsCoverSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Cover Series", "cover-series")
	withSeriesDir(t, storage, "Cover Series")

	body := []byte("fake-png-bytes")
	srv := coverServer(t, body)

	registry := metadata.NewRegistry() // SetCover needs no providers
	svc := metadatasvc.NewService(db, registry, storage)

	coverURL := srv.URL + "/cover.png"
	if err := svc.SetCover(ctx, id, "metadata", "anilist", coverURL); err != nil {
		t.Fatalf("SetCover: %v", err)
	}

	row := db.Series.GetX(ctx, id)
	assertCoverSourceColumns(t, row, coverURL)

	data, ext, err := disk.ReadCoverFile(storage, "Manga", "Cover Series", row.CoverFile)
	if err != nil {
		t.Fatalf("ReadCoverFile: %v", err)
	}
	if string(data) != string(body) {
		t.Errorf("cached cover bytes = %q, want %q", data, body)
	}
	if ext != "png" {
		t.Errorf("cached cover ext = %q, want png (from Content-Type)", ext)
	}
}

// assertCoverSourceColumns is a standalone helper (not a closure) so its own
// branches count toward ITS complexity budget, not the calling test's.
func assertCoverSourceColumns(t *testing.T, row *ent.Series, coverURL string) {
	t.Helper()
	if row.CoverSource == nil || row.CoverSource.Kind != "metadata" || row.CoverSource.Ref != "anilist" {
		t.Fatalf("Series.CoverSource = %+v, want {metadata anilist ...}", row.CoverSource)
	}
	if row.CoverSource.RemoteURL != coverURL {
		t.Errorf("CoverSource.RemoteURL = %q, want %q", row.CoverSource.RemoteURL, coverURL)
	}
	if row.CoverSourceURL != coverURL {
		t.Errorf("CoverSourceURL = %q, want %q", row.CoverSourceURL, coverURL)
	}
	if row.CoverFile == "" {
		t.Fatal("CoverFile is empty, want a cached filename")
	}
	if row.CoverVersion == "" {
		t.Error("CoverVersion is empty, want a content hash")
	}
}

// TestSetCover_NoSeriesDirPropagatesError confirms SetCover — unlike
// persist's best-effort cover step inside AutoIdentify/Identify — RETURNS a
// cover failure to the caller: the whole point of the call is to change the
// cover, so a series with no folder on disk (SaveCover never creates one)
// must surface, not silently no-op.
func TestSetCover_NoSeriesDirPropagatesError(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "No Folder Series", "no-folder-series")
	// Deliberately no withSeriesDir call.

	srv := coverServer(t, []byte("bytes"))
	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.SetCover(ctx, id, "metadata", "anilist", srv.URL+"/cover.png")
	if err == nil {
		t.Fatal("SetCover with no series folder: want an error, got nil")
	}
	if !errors.Is(err, disk.ErrNoSeriesDir) {
		t.Fatalf("SetCover error = %v, want it to wrap disk.ErrNoSeriesDir", err)
	}
}

// TestSetCover_UnknownSeriesReturnsErrSeriesNotFound confirms the sentinel.
func TestSetCover_UnknownSeriesReturnsErrSeriesNotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.SetCover(ctx, randomUUID(), "metadata", "anilist", "https://example.com/cover.png")
	if !errors.Is(err, metadatasvc.ErrSeriesNotFound) {
		t.Fatalf("SetCover(unknown series) error = %v, want ErrSeriesNotFound", err)
	}
}

// TestCoverCandidates_AggregatesNonEmptyCoversFromSearch confirms
// CoverCandidates reuses Registry.Search's fan-out and filters to hits that
// actually carry a cover URL.
func TestCoverCandidates_AggregatesNonEmptyCoversFromSearch(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Gallery Series", "gallery-series")

	withCover := &fakeProvider{
		key: "anilist", priority: 0,
		searchResults: []metadata.SearchResult{
			{Provider: "anilist", RemoteID: "1", CoverURL: "https://covers.example/a.jpg"},
		},
	}
	withoutCover := &fakeProvider{
		key: "mangadex", priority: 1,
		searchResults: []metadata.SearchResult{
			{Provider: "mangadex", RemoteID: "2", CoverURL: ""},
		},
	}
	registry := metadata.NewRegistry(withCover, withoutCover)
	svc := metadatasvc.NewService(db, registry, storage)

	got, err := svc.CoverCandidates(ctx, id)
	if err != nil {
		t.Fatalf("CoverCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1 (the cover-less mangadex hit excluded): %+v", len(got), got)
	}
	if got[0].SourceKind != "metadata" || got[0].SourceRef != "anilist" || got[0].CoverURL != "https://covers.example/a.jpg" {
		t.Errorf("candidate = %+v, want the anilist hit", got[0])
	}
}

// TestCoverCandidates_UnknownSeriesReturnsErrSeriesNotFound confirms the
// sentinel.
func TestCoverCandidates_UnknownSeriesReturnsErrSeriesNotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage)

	_, err := svc.CoverCandidates(ctx, randomUUID())
	if !errors.Is(err, metadatasvc.ErrSeriesNotFound) {
		t.Fatalf("CoverCandidates(unknown series) error = %v, want ErrSeriesNotFound", err)
	}
}
