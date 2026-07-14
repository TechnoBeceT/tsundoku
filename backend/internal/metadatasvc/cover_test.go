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
	// WithHTTPClient is the test seam (service.go): the PRODUCTION default
	// client (newSSRFSafeHTTPClient) refuses to dial 127.0.0.1, which is
	// exactly where coverServer listens — a plain client reaches it.
	svc := metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))

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
	svc := metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))

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

// TestCoverCandidates_IncludesSourceCandidates confirms Feature 2: a
// series' own library SOURCES (SeriesProvider rows with a stored cover_url)
// are appended to the metadata-provider gallery as "source"-kind candidates,
// with the CoverURL rewritten to the browser-loadable per-provider cover
// PROXY path — never the raw Suwayomi-relative cover_url — and Label
// resolved from the provider's display name.
func TestCoverCandidates_IncludesSourceCandidates(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Source Gallery Series", "source-gallery-series")
	providerID := seedSeriesProviderWithCover(ctx, t, db, id, "42", "Comix", "/api/v1/manga/42/thumbnail")

	// No metadata provider registered — proves the source candidate surfaces
	// even when the metadata half of the gallery is empty.
	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage)

	got, err := svc.CoverCandidates(ctx, id)
	if err != nil {
		t.Fatalf("CoverCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1 (the source row): %+v", len(got), got)
	}

	want := metadata.CoverCandidate{
		SourceKind: "source",
		SourceRef:  providerID.String(),
		CoverURL:   "/api/series/" + id.String() + "/providers/" + providerID.String() + "/cover",
		Label:      "Comix",
	}
	if got[0] != want {
		t.Errorf("candidate = %+v, want %+v", got[0], want)
	}
}

// TestCoverCandidates_SourceWithoutCoverURLExcluded confirms a SeriesProvider
// row with no cover_url (the common case — most sources never populate it)
// is not offered as a candidate, mirroring the metadata half's own
// cover-less-hit skip.
func TestCoverCandidates_SourceWithoutCoverURLExcluded(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "No Cover Source Series", "no-cover-source-series")
	seedSeriesProviderWithCover(ctx, t, db, id, "7", "WeebCentral", "")

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage)

	got, err := svc.CoverCandidates(ctx, id)
	if err != nil {
		t.Fatalf("CoverCandidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d candidates, want 0 (cover-less source excluded): %+v", len(got), got)
	}
}

// TestCoverCandidates_SourceLabelFallsBackToProviderKey confirms a source
// with no captured provider_name falls back to the raw provider identity key
// (mirrors series.ProviderLabel's own fallback).
func TestCoverCandidates_SourceLabelFallsBackToProviderKey(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Unnamed Source Series", "unnamed-source-series")
	seedSeriesProviderWithCover(ctx, t, db, id, "unnamed-provider-key", "", "/api/v1/manga/1/thumbnail")

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage)

	got, err := svc.CoverCandidates(ctx, id)
	if err != nil {
		t.Fatalf("CoverCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Label != "unnamed-provider-key" {
		t.Fatalf("candidates = %+v, want one with Label \"unnamed-provider-key\"", got)
	}
}

// TestSetCover_SourceFetchesViaPortAndSaves is Feature 2's SetCover
// round-trip: a "source"-kind pick resolves the SeriesProvider UUID through
// the attached SourceCoverFetcher (NOT an HTTP GET of coverURL — the proxy
// path is not independently fetchable), caches the returned bytes, and
// records cover_source = {source, <providerID>, coverURL}.
func TestSetCover_SourceFetchesViaPortAndSaves(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Source Cover Series", "source-cover-series")
	withSeriesDir(t, storage, "Source Cover Series")
	providerID := seedSeriesProviderWithCover(ctx, t, db, id, "42", "Comix", "/api/v1/manga/42/thumbnail")

	body := []byte("fake-source-cover-bytes")
	scf := &fakeSourceCoverFetcher{seriesID: id, providerID: providerID, data: body, ext: "jpg"}

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage).WithSourceCoverFetcher(scf)

	proxyURL := "/api/series/" + id.String() + "/providers/" + providerID.String() + "/cover"
	if err := svc.SetCover(ctx, id, "source", providerID.String(), proxyURL); err != nil {
		t.Fatalf("SetCover: %v", err)
	}
	if scf.calls != 1 {
		t.Errorf("SourceCoverBytes calls = %d, want 1", scf.calls)
	}

	row := db.Series.GetX(ctx, id)
	if row.CoverSource == nil || row.CoverSource.Kind != "source" || row.CoverSource.Ref != providerID.String() {
		t.Fatalf("Series.CoverSource = %+v, want {source %s ...}", row.CoverSource, providerID)
	}
	if row.CoverSource.RemoteURL != proxyURL {
		t.Errorf("CoverSource.RemoteURL = %q, want %q", row.CoverSource.RemoteURL, proxyURL)
	}

	data, ext, err := disk.ReadCoverFile(storage, "Manga", "Source Cover Series", row.CoverFile)
	if err != nil {
		t.Fatalf("ReadCoverFile: %v", err)
	}
	if string(data) != string(body) {
		t.Errorf("cached cover bytes = %q, want %q", data, body)
	}
	if ext != "jpg" {
		t.Errorf("cached cover ext = %q, want jpg", ext)
	}
}

// TestSetCover_SourceNilFetcherReturnsCleanError confirms a Service built
// without WithSourceCoverFetcher (the plain NewService path every non-source
// test in this file uses) fails a "source"-kind SetCover with a clear,
// wrapped sentinel — never a nil-pointer panic.
func TestSetCover_SourceNilFetcherReturnsCleanError(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "No Fetcher Series", "no-fetcher-series")
	withSeriesDir(t, storage, "No Fetcher Series")
	providerID := seedSeriesProviderWithCover(ctx, t, db, id, "42", "Comix", "/api/v1/manga/42/thumbnail")

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage) // no WithSourceCoverFetcher

	err := svc.SetCover(ctx, id, "source", providerID.String(), "/api/series/"+id.String()+"/providers/"+providerID.String()+"/cover")
	if !errors.Is(err, metadatasvc.ErrSourceCoverFetcherNotConfigured) {
		t.Fatalf("SetCover(source, no fetcher) error = %v, want ErrSourceCoverFetcherNotConfigured", err)
	}
}

// TestSetCover_SourceInvalidRefReturnsError confirms a non-UUID sourceRef
// (e.g. a caller mixing up a metadata provider key with a source) fails
// cleanly rather than panicking on uuid.MustParse or reaching the fetcher.
func TestSetCover_SourceInvalidRefReturnsError(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Bad Ref Series", "bad-ref-series")
	withSeriesDir(t, storage, "Bad Ref Series")

	scf := &fakeSourceCoverFetcher{}
	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage).WithSourceCoverFetcher(scf)

	err := svc.SetCover(ctx, id, "source", "not-a-uuid", "/api/series/"+id.String()+"/providers/not-a-uuid/cover")
	if err == nil {
		t.Fatal("SetCover(source, invalid ref): want an error, got nil")
	}
	if scf.calls != 0 {
		t.Errorf("SourceCoverBytes calls = %d, want 0 (should fail before reaching the port)", scf.calls)
	}
}

// TestSetCover_SourcePortFailurePropagates confirms a SourceCoverFetcher
// failure (e.g. the provider does not belong to the series, or a Suwayomi
// fetch failure) is returned to the caller, not swallowed.
func TestSetCover_SourcePortFailurePropagates(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Port Failure Series", "port-failure-series")
	withSeriesDir(t, storage, "Port Failure Series")
	providerID := seedSeriesProviderWithCover(ctx, t, db, id, "42", "Comix", "/api/v1/manga/42/thumbnail")

	scf := &fakeSourceCoverFetcher{err: errors.New("boom")}
	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage).WithSourceCoverFetcher(scf)

	err := svc.SetCover(ctx, id, "source", providerID.String(), "/api/series/"+id.String()+"/providers/"+providerID.String()+"/cover")
	if err == nil {
		t.Fatal("SetCover(source, failing port): want an error, got nil")
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

// TestSetCover_ProductionClientBlocksLoopback is the SSRF integration proof:
// a Service built via the PRODUCTION constructor path — metadatasvc.NewService
// with NO WithHTTPClient option, so it gets the real newSSRFSafeHTTPClient —
// refuses to fetch a cover from a 127.0.0.1 httptest.Server. This proves the
// guard is actually wired into the client production callers get, not merely
// unit-tested against isBlockedIP in isolation. Contrast every other test in
// this file, which passes WithHTTPClient(&http.Client{}) and DOES reach the
// same kind of local server.
func TestSetCover_ProductionClientBlocksLoopback(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "SSRF Series", "ssrf-series")
	withSeriesDir(t, storage, "SSRF Series")

	srv := coverServer(t, []byte("fake-png-bytes"))

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage) // no WithHTTPClient — the production client.

	err := svc.SetCover(ctx, id, "metadata", "anilist", srv.URL+"/cover.png")
	if err == nil {
		t.Fatal("SetCover via the production client against a 127.0.0.1 server: want an error (SSRF-blocked), got nil")
	}
}

// TestSetCover_OversizeBodyRejected confirms fetchCoverBytes' bounded read:
// a response streaming more than maxCoverBytes (20 MiB) is rejected rather
// than buffered wholesale into memory.
func TestSetCover_OversizeBodyRejected(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Oversize Series", "oversize-series")
	withSeriesDir(t, storage, "Oversize Series")

	const oneMiB = 1 << 20
	chunk := make([]byte, oneMiB)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		// Stream 21 MiB with no Content-Length header (chunked), so the
		// oversize is caught by the body-read cap, not the fast Content-Length
		// pre-check — exercising the other guard.
		for range 21 {
			_, _ = w.Write(chunk)
		}
	}))
	t.Cleanup(srv.Close)

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))

	err := svc.SetCover(ctx, id, "metadata", "anilist", srv.URL+"/cover.png")
	if err == nil {
		t.Fatal("SetCover with a 21 MiB body: want an error (too large), got nil")
	}
}

// TestSetCover_OversizeContentLengthRejectedFast confirms the fast
// pre-check: a DECLARED Content-Length over the cap is rejected before the
// body is ever read.
func TestSetCover_OversizeContentLengthRejectedFast(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Declared Oversize Series", "declared-oversize-series")
	withSeriesDir(t, storage, "Declared Oversize Series")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "31457280") // 30 MiB declared, over the 20 MiB cap.
		w.WriteHeader(http.StatusOK)
		// The handler need not actually write 30 MiB: fetchCoverBytes returns
		// on the Content-Length check before ever reading the body, and the
		// client aborts the connection on return.
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))

	err := svc.SetCover(ctx, id, "metadata", "anilist", srv.URL+"/cover.png")
	if err == nil {
		t.Fatal("SetCover with a declared 30 MiB Content-Length: want an error, got nil")
	}
}

// TestSetCover_NonImageContentTypeRejected confirms a response that is
// clearly not an image (declared text/html, body is not image bytes) is
// rejected rather than cached as a bogus "cover".
func TestSetCover_NonImageContentTypeRejected(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "HTML Series", "html-series")
	withSeriesDir(t, storage, "HTML Series")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
	}))
	t.Cleanup(srv.Close)

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))

	err := svc.SetCover(ctx, id, "metadata", "anilist", srv.URL+"/cover.png")
	if err == nil {
		t.Fatal("SetCover with a text/html response: want an error (not an image), got nil")
	}
}

// TestSetCover_MissingContentTypeStillAcceptedWhenBytesSniffAsImage confirms
// the sniff fallback: a response with NO Content-Type header at all is still
// accepted when its bytes are a real image (providers vary in whether they
// set the header) — a missing header must never be treated the same as a
// declared non-image type.
func TestSetCover_MissingContentTypeStillAcceptedWhenBytesSniffAsImage(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Sniffed Series", "sniffed-series")
	withSeriesDir(t, storage, "Sniffed Series")

	// A minimal valid PNG signature + IHDR-ish bytes is enough for
	// http.DetectContentType to sniff "image/png".
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately no Content-Type header set.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)

	registry := metadata.NewRegistry()
	svc := metadatasvc.NewService(db, registry, storage, metadatasvc.WithHTTPClient(&http.Client{}))

	if err := svc.SetCover(ctx, id, "metadata", "anilist", srv.URL+"/cover.png"); err != nil {
		t.Fatalf("SetCover with a headerless-but-real-PNG response: want success, got %v", err)
	}
}
