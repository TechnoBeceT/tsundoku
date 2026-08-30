package enginetopo

// White-box tests for the .apk download size cap (the cappedReader guard +
// downloadAndCache wiring). They live in `package enginetopo` because both the
// reader and downloadAndCache are unexported, and they need neither Postgres nor
// the JVM — just an in-memory apk cache and a stub httpGet.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
)

func TestFetchIndexCancellationInterruptsStalledHeaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := fetchIndex(ctx, func(callCtx context.Context, _ string) (*http.Response, error) {
			close(started)
			<-callCtx.Done()
			return nil, callCtx.Err()
		}, "https://repo.test")
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetchIndex error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fetchIndex did not stop after cancellation")
	}
}

type cancellationBody struct{ ctx context.Context }

func (b cancellationBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (cancellationBody) Close() error               { return nil }

func TestDownloadAndCacheCancellationInterruptsStalledBody(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := downloadAndCache(ctx, apkcache.New(t.TempDir()), func(callCtx context.Context, _ string) (*http.Response, error) {
			close(started)
			return &http.Response{StatusCode: http.StatusOK, Body: cancellationBody{ctx: callCtx}}, nil
		}, "https://repo.test/a.apk", "pkg", 1, 1024)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("downloadAndCache error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("downloadAndCache did not stop after cancellation")
	}
}

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

// TestRepoURLResolution covers repoBaseURL / indexURLFor / apkURLFor across every
// stored repo-URL shape the engine hands us: an index-FILE URL (index.pb — THE PROD
// REGRESSION CASE — and index.min.json), a bare repo directory (with and without a
// trailing slash), and a nested path. Deriving the base directory first is what
// makes an index-file URL resolve to "<base>/index.min.json" instead of the old
// ".../index.pb/index.min.json" 404.
func TestRepoURLResolution(t *testing.T) {
	tests := []struct {
		name      string
		repoURL   string
		wantBase  string
		wantIndex string
		wantAPK   string
	}{
		{
			// THE PROD REGRESSION CASE: the engine stores the repo's index FILE URL
			// (keiyoushi uses the protobuf index.pb); the base is ".../repo".
			name:      "index.pb file url",
			repoURL:   "https://x/repo/index.pb",
			wantBase:  "https://x/repo",
			wantIndex: "https://x/repo/index.min.json",
			wantAPK:   "https://x/repo/apk/a.apk",
		},
		{
			name:      "index.min.json file url",
			repoURL:   "https://x/repo/index.min.json",
			wantBase:  "https://x/repo",
			wantIndex: "https://x/repo/index.min.json",
			wantAPK:   "https://x/repo/apk/a.apk",
		},
		{
			name:      "bare repo directory",
			repoURL:   "https://x/repo",
			wantBase:  "https://x/repo",
			wantIndex: "https://x/repo/index.min.json",
			wantAPK:   "https://x/repo/apk/a.apk",
		},
		{
			name:      "bare repo directory trailing slash",
			repoURL:   "https://x/repo/",
			wantBase:  "https://x/repo",
			wantIndex: "https://x/repo/index.min.json",
			wantAPK:   "https://x/repo/apk/a.apk",
		},
		{
			name:      "nested path index file",
			repoURL:   "https://x/a/b/repo/index.pb",
			wantBase:  "https://x/a/b/repo",
			wantIndex: "https://x/a/b/repo/index.min.json",
			wantAPK:   "https://x/a/b/repo/apk/a.apk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoBaseURL(tt.repoURL); got != tt.wantBase {
				t.Errorf("repoBaseURL(%q) = %q, want %q", tt.repoURL, got, tt.wantBase)
			}
			if got := repoBaseFor(tt.repoURL); got != tt.wantBase {
				t.Errorf("repoBaseFor(%q) = %q, want %q", tt.repoURL, got, tt.wantBase)
			}
			if got := indexURLFor(tt.repoURL); got != tt.wantIndex {
				t.Errorf("indexURLFor(%q) = %q, want %q", tt.repoURL, got, tt.wantIndex)
			}
			if got := apkURLFor(tt.repoURL, "a.apk"); got != tt.wantAPK {
				t.Errorf("apkURLFor(%q, a.apk) = %q, want %q", tt.repoURL, got, tt.wantAPK)
			}
		})
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

	httpGet := func(context.Context, string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("A", 100))),
		}, nil
	}

	// A 10-byte cap against a 100-byte body must fail.
	if _, err := downloadAndCache(context.Background(), cache, httpGet, apkURL, "pkg.huge", 1, 10); err == nil {
		t.Fatal("downloadAndCache: want error for an oversized body, got nil")
	}
	if cache.Exists("pkg.huge", 1) {
		t.Error("cache holds pkg.huge after an oversized download, want nothing cached")
	}
}
