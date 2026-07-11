package series_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// countingPageBytes wires env.sw to return the given image and counts every call.
func countingPageBytes(env *testEnv, data []byte, ext string) *atomic.Int32 {
	var calls atomic.Int32
	env.sw.pageBytes = func(context.Context, string) ([]byte, string, error) {
		calls.Add(1)
		return data, ext, nil
	}
	return &calls
}

// doWithHeader issues an authed GET carrying one extra header (If-None-Match).
func (env *testEnv) doWithHeader(target, key, value string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	r.Header.Set(key, value)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// coverVersionOf reads the version the DTO advertises for the series — the value
// a real client would put in ?v=.
func coverVersionOf(t *testing.T, env *testEnv, seriesID string) string {
	t.Helper()
	rec := env.do(http.MethodGet, "/api/series/"+seriesID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GetSeries: want 200, got %d", rec.Code)
	}
	var body struct {
		CoverURL string `json:"coverUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode series detail: %v", err)
	}
	_, version, found := strings.Cut(body.CoverURL, "?v=")
	if !found {
		return ""
	}
	return version
}

// TestSeriesCover_MatchingVersionIsImmutable proves a request carrying the
// CURRENT content version is hard-cacheable. That is safe only because the
// version hashes the image BYTES: the URL changes whenever the cover does, so an
// immutable response can never pin a stale image.
func TestSeriesCover_MatchingVersionIsImmutable(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	countingPageBytes(env, []byte("IMG"), "png")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	// Warm the cache so the series has a cached cover (and therefore a version).
	if rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover", ""); rec.Code != http.StatusOK {
		t.Fatalf("SeriesCover (warming): want 200, got %d", rec.Code)
	}
	version := coverVersionOf(t, env, seriesID.String())
	if version == "" {
		t.Fatal("cached cover has no version in its coverUrl")
	}

	rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover?v="+version, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("SeriesCover: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want private, max-age=31536000, immutable", cc)
	}
	if etag := rec.Header().Get("ETag"); etag != `"`+version+`"` {
		t.Errorf("ETag = %q, want the server's version %q", etag, version)
	}
}

// TestSeriesCover_UnversionedRequestIsNeverImmutable proves an UNVERSIONED (or
// wrongly-versioned) request — a bookmark, a curl, an <img> preload, the service
// worker — still 200s but is only ever revalidatable.
//
// Marking those immutable would permanently poison a URL that carries NO cache
// buster: there would be no lever left to ever show a new cover.
func TestSeriesCover_UnversionedRequestIsNeverImmutable(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	countingPageBytes(env, []byte("IMG"), "png")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	for _, target := range []string{
		"/api/series/" + seriesID.String() + "/cover",
		"/api/series/" + seriesID.String() + "/cover?v=not-the-version",
	} {
		rec := env.do(http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("SeriesCover %s: want 200, got %d (%s)", target, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != "IMG" {
			t.Errorf("SeriesCover %s: body = %q, want IMG", target, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-cache" {
			t.Errorf("SeriesCover %s: Cache-Control = %q, want private, no-cache", target, cc)
		}
	}
}

// TestSeriesCover_ETagTracksTheCoverVersion proves the validator follows the
// cover's CONTENT: new bytes under the same (id-derived, unchanging) cover_url
// must yield a new ETag, or a client holding the old one would win a spurious 304
// on an image that has actually changed.
func TestSeriesCover_ETagTracksTheCoverVersion(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	countingPageBytes(env, []byte("OLD-ART"), "jpg")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	target := "/api/series/" + seriesID.String() + "/cover"
	first := env.do(http.MethodGet, target, "")
	firstETag := first.Header().Get("ETag")
	if firstETag == "" {
		t.Fatal("SeriesCover: missing ETag")
	}

	// The local file is lost (an NFS blip) and the source now serves new art under
	// the SAME cover_url.
	if err := os.Remove(filepath.Join(env.storage, "Manga", "Cover Test", "cover.jpg")); err != nil {
		t.Fatalf("remove cover file: %v", err)
	}
	countingPageBytes(env, []byte("NEW-ART"), "jpg")

	second := env.do(http.MethodGet, target, "")
	if second.Body.String() != "NEW-ART" {
		t.Fatalf("SeriesCover: body = %q, want NEW-ART", second.Body.String())
	}
	if got := second.Header().Get("ETag"); got == firstETag {
		t.Fatalf("ETag unchanged (%s) after the cover bytes changed", got)
	}
}

// TestSeriesCover_IfNoneMatch304 proves a repeat view revalidates to a bodyless
// 304 instead of re-transferring the image.
func TestSeriesCover_IfNoneMatch304(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	countingPageBytes(env, []byte("IMG"), "png")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	target := "/api/series/" + seriesID.String() + "/cover"
	first := env.do(http.MethodGet, target, "")
	etag := first.Header().Get("ETag")

	rec := env.doWithHeader(target, "If-None-Match", etag)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("SeriesCover If-None-Match: want 304, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("SeriesCover 304: body must be empty, got %d bytes", rec.Body.Len())
	}
}

// TestSeriesCover_CachedServeMakesZeroSuwayomiCalls is THE anti-ban proof at the
// HTTP boundary: once the cover is cached, serving it again pings Suwayomi ZERO
// times. This is the whole feature — a 52-series grid re-render costs no
// source-ward traffic at all.
func TestSeriesCover_CachedServeMakesZeroSuwayomiCalls(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	calls := countingPageBytes(env, []byte("IMG"), "jpg")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	target := "/api/series/" + seriesID.String() + "/cover"
	if rec := env.do(http.MethodGet, target, ""); rec.Code != http.StatusOK {
		t.Fatalf("SeriesCover (warming): want 200, got %d", rec.Code)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cold serve: Suwayomi calls = %d, want 1", got)
	}
	calls.Store(0)

	for i := range 3 {
		rec := env.do(http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("SeriesCover (cached, i=%d): want 200, got %d", i, rec.Code)
		}
		if rec.Body.String() != "IMG" {
			t.Fatalf("SeriesCover (cached): body = %q, want IMG", rec.Body.String())
		}
	}

	if got := calls.Load(); got != 0 {
		t.Fatalf("ANTI-BAN PROOF FAILED: cached serves made %d Suwayomi call(s), want 0", got)
	}
}

// TestSeriesCover_DiskWriteFailureStillServes proves an unwritable library root
// (a cache that cannot persist) still yields the image, not an error page.
func TestSeriesCover_DiskWriteFailureStillServes(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	countingPageBytes(env, []byte("IMG"), "png")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	// The series folder exists but is not writable ⇒ the cache write fails.
	seriesDir := filepath.Join(env.storage, "Manga", "Cover Test")
	// G302: a read-only DIRECTORY (r-x) is exactly what makes the cache write fail;
	// dir modes legitimately need the exec bit, and this is test-only.
	if err := os.Chmod(seriesDir, 0o500); err != nil { //nolint:gosec
		t.Fatalf("chmod series dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(seriesDir, 0o750) }) //nolint:gosec

	rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("SeriesCover (unwritable cache): want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "IMG" {
		t.Errorf("SeriesCover (unwritable cache): body = %q, want IMG", rec.Body.String())
	}
}

// TestSeriesCover_ColdFetchFailureIs502 proves a Suwayomi failure on a cold
// cover is a gateway error, never a false 200 (errors.New keeps the import used).
func TestSeriesCover_ColdFetchFailureIs502(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.sw.pageBytes = func(context.Context, string) ([]byte, string, error) {
		return nil, "", errors.New("suwayomi down")
	}
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("SeriesCover (cold fetch failure): want 502, got %d", rec.Code)
	}
}
