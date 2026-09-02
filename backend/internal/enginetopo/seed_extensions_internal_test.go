package enginetopo

// White-box tests for the .apk download size cap (the cappedReader guard +
// downloadAndCache wiring). They live in `package enginetopo` because both the
// reader and downloadAndCache are unexported, and they need neither Postgres nor
// the JVM — just an in-memory apk cache and a stub httpGet.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
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
// stored repo-URL shape the engine hands us. Explicit index files must be fetched
// verbatim so their format and catalogue are preserved; only a directory URL gets
// the legacy index.min.json default. APK paths still hang off the common base.
func TestRepoURLResolution(t *testing.T) {
	tests := []struct {
		name      string
		repoURL   string
		wantBase  string
		wantIndex string
		wantAPK   string
	}{
		{
			name:      "index.pb file url",
			repoURL:   "https://x/repo/index.pb",
			wantBase:  "https://x/repo",
			wantIndex: "https://x/repo/index.pb",
			wantAPK:   "https://x/repo/apk/a.apk",
		},
		{
			name:      "index.json file url",
			repoURL:   "https://x/repo/index.json",
			wantBase:  "https://x/repo",
			wantIndex: "https://x/repo/index.json",
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
			wantIndex: "https://x/a/b/repo/index.pb",
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

// TestIndexResolverSupportedFormats pins the four repository shapes accepted by
// the engine host: current wrapper JSON, legacy top-level arrays, an unadorned
// directory (which defaults to index.min.json), and protobuf. All normalize to
// the same absolute APK URL + numeric version contract.
func TestIndexResolverSupportedFormats(t *testing.T) {
	protobufBody := gzipBytes(t, testRepoIndexPB("pkg.protobuf", "https://cdn.test/protobuf-v8.apk", 8))
	tests := []struct {
		name      string
		repoURL   string
		wantFetch string
		body      []byte
		pkg       string
		wantAPK   string
		wantCode  int
	}{
		{
			name:      "configured wrapper index json",
			repoURL:   "https://repo.test/index.json",
			wantFetch: "https://repo.test/index.json",
			body:      []byte(`{"extensionList":{"extensions":[{"packageName":"pkg.wrapper","versionCode":"12","resources":{"apkUrl":"https://cdn.test/wrapper-v12.apk"}}]}}`),
			pkg:       "pkg.wrapper",
			wantAPK:   "https://cdn.test/wrapper-v12.apk",
			wantCode:  12,
		},
		{
			name:      "explicit legacy index min json",
			repoURL:   "https://repo.test/index.min.json",
			wantFetch: "https://repo.test/index.min.json",
			body:      []byte(`[{"pkg":"pkg.legacy","apk":"legacy-v7.apk","code":7}]`),
			pkg:       "pkg.legacy",
			wantAPK:   "https://repo.test/apk/legacy-v7.apk",
			wantCode:  7,
		},
		{
			name:      "directory defaults to legacy index",
			repoURL:   "https://repo.test/catalogue/",
			wantFetch: "https://repo.test/catalogue/index.min.json",
			body:      []byte(`[{"pkg":"pkg.directory","apk":"directory-v4.apk","code":4}]`),
			pkg:       "pkg.directory",
			wantAPK:   "https://repo.test/catalogue/apk/directory-v4.apk",
			wantCode:  4,
		},
		{
			name:      "configured gzip protobuf index",
			repoURL:   "https://repo.test/index.pb",
			wantFetch: "https://repo.test/index.pb",
			body:      protobufBody,
			pkg:       "pkg.protobuf",
			wantAPK:   "https://cdn.test/protobuf-v8.apk",
			wantCode:  8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fetched string
			httpGet := func(_ context.Context, url string) (*http.Response, error) {
				fetched = url
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(tt.body))}, nil
			}
			gotAPK, gotCode, err := newIndexResolver(context.Background(), httpGet).resolve(tt.repoURL, tt.pkg)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if fetched != tt.wantFetch {
				t.Errorf("fetched URL = %q, want %q", fetched, tt.wantFetch)
			}
			if gotAPK != tt.wantAPK || gotCode != tt.wantCode {
				t.Errorf("resolved = {%q,%d}, want {%q,%d}", gotAPK, gotCode, tt.wantAPK, tt.wantCode)
			}
		})
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("gzip fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return compressed.Bytes()
}

// testRepoIndexPB emits the small subset of the current protobuf schema this
// package consumes: root.extensionList(101) -> extensions(1) -> packageName(2),
// resources.apkUrl(3.1), versionCode(5). It is intentionally hand-encoded so
// this compatibility test needs no generated protobuf code.
func testRepoIndexPB(pkg, apkURL string, version uint64) []byte {
	resources := appendProtoBytes(nil, 1, []byte(apkURL))
	extension := appendProtoBytes(nil, 2, []byte(pkg))
	extension = appendProtoBytes(extension, 3, resources)
	extension = appendProtoVarint(extension, 5, version)
	list := appendProtoBytes(nil, 1, extension)
	return appendProtoBytes(nil, 101, list)
}

func appendProtoBytes(dst []byte, field uint64, value []byte) []byte {
	dst = binary.AppendUvarint(dst, field<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendProtoVarint(dst []byte, field, value uint64) []byte {
	dst = binary.AppendUvarint(dst, field<<3)
	return binary.AppendUvarint(dst, value)
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
