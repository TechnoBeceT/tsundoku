package enginetopo

// White-box tests for the .apk download size cap (the cappedReader guard +
// downloadAndCache wiring). They live in `package enginetopo` because both the
// reader and downloadAndCache are unexported, and they need neither Postgres nor
// the JVM — just an in-memory apk cache and a stub httpGet.

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
)

// TestCappedReader_ErrorsPastCapWithoutTruncating proves the reader surfaces
// errAPKTooLarge once its cap is exceeded (fail-clean), while a stream exactly AT
// the cap is read through untouched — the boundary that separates a real
// oversized body from a legitimate one.
func TestCappedReader_ErrorsPastCapWithoutTruncating(t *testing.T) {
	// 10 bytes through a 4-byte cap → error, never a silent truncation.
	over := &cappedReader{r: strings.NewReader("0123456789"), max: 4}
	if _, err := io.ReadAll(over); !errors.Is(err, errAPKTooLarge) {
		t.Fatalf("ReadAll(over-cap) error = %v, want errAPKTooLarge", err)
	}

	// Exactly at the cap → accepted in full (read == max is not "over").
	atCap := &cappedReader{r: strings.NewReader("ABCD"), max: 4}
	got, err := io.ReadAll(atCap)
	if err != nil {
		t.Fatalf("ReadAll(at-cap) error = %v, want nil", err)
	}
	if string(got) != "ABCD" {
		t.Errorf("at-cap read = %q, want %q", got, "ABCD")
	}
}

// TestDownloadAndCache_OversizedBodyErrorsAndCachesNothing proves the .apk size
// ceiling end-to-end: a body larger than maxBytes makes downloadAndCache return
// an error and leaves NOTHING in the cache (cache.Put drops its temp file on the
// read error), so a hostile/broken repo can neither fill the volume nor cache a
// corrupt partial file.
func TestDownloadAndCache_OversizedBodyErrorsAndCachesNothing(t *testing.T) {
	cache := apkcache.New(t.TempDir())
	const apkURL = "https://repo.test/repo/apk/huge.apk"

	httpGet := func(string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("A", 100))),
		}, nil
	}

	// A 10-byte cap against a 100-byte body must fail.
	if _, err := downloadAndCache(cache, httpGet, apkURL, "pkg.huge", 1, 10); err == nil {
		t.Fatal("downloadAndCache: want error for an oversized body, got nil")
	}
	if cache.Exists("pkg.huge", 1) {
		t.Error("cache holds pkg.huge after an oversized download, want nothing cached")
	}
}
