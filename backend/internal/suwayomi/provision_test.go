package suwayomi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fakeJARBytes is the fake JAR content served by the test server.
var fakeJARBytes = []byte("PK\x03\x04fake-jar-content")

// newTestServer builds an httptest.Server that serves fakeJARBytes on any path
// and records the number of times it has been called.
func newTestServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/java-archive")
		_, _ = w.Write(fakeJARBytes)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// testCfg returns a SuwayomiConfig whose DownloadURLTemplate points at the
// given httptest server. RuntimeDir is a fresh t.TempDir() each call.
func testCfg(t *testing.T, srv *httptest.Server) config.SuwayomiConfig {
	t.Helper()
	// Template: the two %s placeholders are filled with the version tag.
	// We point them at the test server; the path is ignored by the fake handler.
	return config.SuwayomiConfig{
		Version:             "v9.9.9999",
		RuntimeDir:          t.TempDir(),
		DownloadURLTemplate: srv.URL + "/releases/download/%s/Suwayomi-Server-%s.jar",
		DownloadTimeout:     30 * time.Second,
	}
}

// TestEnsureJAR_downloads verifies that EnsureJAR fetches the JAR when absent
// and returns a path whose content matches what the server served.
func TestEnsureJAR_downloads(t *testing.T) {
	srv, hits := newTestServer(t)
	cfg := testCfg(t, srv)

	path, err := suwayomi.EnsureJAR(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EnsureJAR returned unexpected error: %v", err)
	}

	// File must exist.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("returned path %q does not exist: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("returned file is empty")
	}

	// Content must match what the server sent.
	// G304: path is returned by EnsureJAR from a t.TempDir() root — not a traversal risk.
	got, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("could not read returned file: %v", err)
	}
	if string(got) != string(fakeJARBytes) {
		t.Fatalf("file content mismatch: got %q, want %q", got, fakeJARBytes)
	}

	// Exactly one download.
	if n := hits.Load(); n != 1 {
		t.Fatalf("expected 1 server hit, got %d", n)
	}
}

// TestEnsureJAR_idempotent verifies that a second call with the same config
// skips the download entirely (server hit count stays at 1).
func TestEnsureJAR_idempotent(t *testing.T) {
	srv, hits := newTestServer(t)
	cfg := testCfg(t, srv)

	path1, err := suwayomi.EnsureJAR(context.Background(), cfg)
	if err != nil {
		t.Fatalf("first EnsureJAR: %v", err)
	}

	stat1, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("stat after first call: %v", err)
	}

	path2, err := suwayomi.EnsureJAR(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second EnsureJAR: %v", err)
	}

	// Same path returned.
	if path1 != path2 {
		t.Fatalf("idempotency: paths differ: %q vs %q", path1, path2)
	}

	// Server was NOT contacted again — this is the non-vacuous assertion.
	if n := hits.Load(); n != 1 {
		t.Fatalf("idempotency: expected 1 server hit total, got %d (re-downloaded on second call)", n)
	}

	// Modification time must be unchanged (the file was not touched).
	stat2, err := os.Stat(path2)
	if err != nil {
		t.Fatalf("stat after second call: %v", err)
	}
	if !stat2.ModTime().Equal(stat1.ModTime()) {
		t.Fatalf("idempotency: mtime changed (%.9s → %.9s); file was overwritten",
			stat1.ModTime(), stat2.ModTime())
	}
}

// TestEnsureJAR_httpError verifies that a non-200 response causes an error and
// leaves no file at the final JAR path.
func TestEnsureJAR_httpError(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", code)
			}))
			t.Cleanup(srv.Close)

			cfg := config.SuwayomiConfig{
				Version:             "v9.9.9999",
				RuntimeDir:          t.TempDir(),
				DownloadURLTemplate: srv.URL + "/releases/download/%s/Suwayomi-Server-%s.jar",
				DownloadTimeout:     30 * time.Second,
			}

			_, err := suwayomi.EnsureJAR(context.Background(), cfg)
			if err == nil {
				t.Fatal("expected error for HTTP error response, got nil")
			}

			// No partial file at the final path.
			finalPath := filepath.Join(cfg.RuntimeDir, "Suwayomi", "Suwayomi-Server-v9.9.9999.jar")
			if _, statErr := os.Stat(finalPath); !os.IsNotExist(statErr) {
				t.Fatalf("partial file left at final path %q after HTTP %d error", finalPath, code)
			}
		})
	}
}

// TestEnsureJAR_emptyBody verifies that a 200 response with an empty body is
// rejected and leaves no file at the final path.
func TestEnsureJAR_emptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Zero bytes — deliberately empty.
	}))
	t.Cleanup(srv.Close)

	cfg := config.SuwayomiConfig{
		Version:             "v9.9.9999",
		RuntimeDir:          t.TempDir(),
		DownloadURLTemplate: srv.URL + "/releases/download/%s/Suwayomi-Server-%s.jar",
		DownloadTimeout:     30 * time.Second,
	}

	_, err := suwayomi.EnsureJAR(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}

	// No partial file at the final path.
	finalPath := filepath.Join(cfg.RuntimeDir, "Suwayomi", "Suwayomi-Server-v9.9.9999.jar")
	if _, statErr := os.Stat(finalPath); !os.IsNotExist(statErr) {
		t.Fatalf("partial file left at final path %q after empty-body response", finalPath)
	}
}

// TestJARFileName verifies that jarFileName is deterministic for a given version.
func TestJARFileName(t *testing.T) {
	const version = "v2.2.2100"
	got1 := suwayomi.JARFileName(version)
	got2 := suwayomi.JARFileName(version)

	if got1 == "" {
		t.Fatal("JARFileName returned empty string")
	}
	if got1 != got2 {
		t.Fatalf("JARFileName is not deterministic: %q vs %q", got1, got2)
	}

	want := "Suwayomi-Server-v2.2.2100.jar"
	if got1 != want {
		t.Fatalf("JARFileName(%q) = %q, want %q", version, got1, want)
	}
}

// TestEnsureJAR_cancelledContext verifies that a pre-cancelled context causes
// EnsureJAR to return an error and leave no file at the final path.
func TestEnsureJAR_cancelledContext(t *testing.T) {
	srv, _ := newTestServer(t)
	cfg := testCfg(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := suwayomi.EnsureJAR(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}

	finalPath := filepath.Join(cfg.RuntimeDir, "Suwayomi", suwayomi.JARFileName(cfg.Version))
	if _, statErr := os.Stat(finalPath); !os.IsNotExist(statErr) {
		t.Fatalf("partial file left at final path %q after cancelled context", finalPath)
	}
}

// TestEnsureJAR_downloadsToCorrectPath verifies that the downloaded JAR lands
// under RuntimeDir/Suwayomi/<jarFileName>.
func TestEnsureJAR_downloadsToCorrectPath(t *testing.T) {
	srv, _ := newTestServer(t)
	cfg := testCfg(t, srv)

	path, err := suwayomi.EnsureJAR(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EnsureJAR: %v", err)
	}

	expectedDir := filepath.Join(cfg.RuntimeDir, "Suwayomi")
	expectedName := suwayomi.JARFileName(cfg.Version)
	expectedPath := filepath.Join(expectedDir, expectedName)

	if path != expectedPath {
		t.Fatalf("returned path %q, want %q", path, expectedPath)
	}

	// The enclosing directory must exist.
	if _, err := os.Stat(expectedDir); err != nil {
		t.Fatalf("directory %q not created: %v", expectedDir, err)
	}
}

// TestEnsureJAR_noTempFileLeft verifies that no .tmp file remains after a
// successful download.
func TestEnsureJAR_noTempFileLeft(t *testing.T) {
	srv, _ := newTestServer(t)
	cfg := testCfg(t, srv)

	path, err := suwayomi.EnsureJAR(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EnsureJAR: %v", err)
	}

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file %q still exists after successful download", tmpPath)
	}
}

// TestEnsureJAR_noTempFileLeftOnError verifies that a .tmp file is not left
// behind after a download failure (HTTP error path).
func TestEnsureJAR_noTempFileLeftOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	t.Cleanup(srv.Close)

	cfg := config.SuwayomiConfig{
		Version:             "v9.9.9999",
		RuntimeDir:          t.TempDir(),
		DownloadURLTemplate: srv.URL + "/releases/download/%s/Suwayomi-Server-%s.jar",
		DownloadTimeout:     30 * time.Second,
	}

	_, _ = suwayomi.EnsureJAR(context.Background(), cfg)

	expectedDir := filepath.Join(cfg.RuntimeDir, "Suwayomi")
	entries, _ := os.ReadDir(expectedDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file %q left behind after download error", filepath.Join(expectedDir, e.Name()))
		}
	}
}
