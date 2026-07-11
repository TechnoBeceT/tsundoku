package series_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestSeriesCover_CacheHeaders proves the 200 carries an ETag and a
// revalidatable (never "immutable") Cache-Control — the cover legitimately
// changes when the owner switches metadata source.
func TestSeriesCover_CacheHeaders(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	countingPageBytes(env, []byte("IMG"), "png")
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")

	rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("SeriesCover: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("SeriesCover: missing ETag")
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "private, max-age=86400" {
		t.Errorf("SeriesCover: Cache-Control = %q, want private, max-age=86400", cc)
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

	// Make the series' category dir un-creatable by taking its name with a file.
	if err := os.WriteFile(filepath.Join(env.storage, "Manga"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

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
