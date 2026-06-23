// Package suwayomi manages the lifecycle of the embedded Suwayomi-Server
// process: provisioning (this file), process management (Task 3), and
// health-check polling.
package suwayomi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
)

// EnsureJAR guarantees that the pinned Suwayomi-Server JAR is present under
// RuntimeDir/Suwayomi/. If a non-empty JAR for cfg.Version already exists, it
// is returned immediately (idempotent — no network request). Otherwise the JAR
// is downloaded from cfg.DownloadURLTemplate (with both %s placeholders filled
// by cfg.Version) under cfg.DownloadTimeout, written atomically via a temp-file
// rename (a failed or partial download never leaves a usable file at the final
// path), and the final path is returned.
//
// The context governs the HTTP request; a cancelled or deadline-exceeded context
// causes an error and no file is written.
func EnsureJAR(ctx context.Context, cfg config.SuwayomiConfig) (jarPath string, err error) {
	dir := filepath.Join(cfg.RuntimeDir, "Suwayomi")
	jarPath = filepath.Join(dir, JARFileName(cfg.Version))

	// Idempotency check: return immediately if a non-empty JAR is already present.
	if info, statErr := os.Stat(jarPath); statErr == nil && info.Size() > 0 {
		return jarPath, nil
	}

	// Ensure the target directory exists before creating any files in it.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("suwayomi.EnsureJAR: create runtime dir: %w", err)
	}

	url := fmt.Sprintf(cfg.DownloadURLTemplate, cfg.Version, cfg.Version)
	if err := downloadJAR(ctx, url, jarPath, cfg.DownloadTimeout); err != nil {
		return "", err
	}

	return jarPath, nil
}

// JARFileName returns the deterministic basename for the Suwayomi-Server JAR
// asset for the given version tag (e.g. "v2.2.2100" → "Suwayomi-Server-v2.2.2100.jar").
// This matches the GitHub release asset naming convention.
func JARFileName(version string) string {
	return "Suwayomi-Server-" + version + ".jar"
}

// downloadJAR downloads the JAR from url into jarPath atomically. It writes to
// a temporary file alongside the final destination, fsyncs the temp file to
// ensure the data is durable, then renames it into place. If any step fails, the
// temporary file is removed so no partial download lingers.
//
// A timeout of zero disables the per-request deadline (context cancellation
// still applies).
func downloadJAR(ctx context.Context, url, jarPath string, timeout time.Duration) error {
	tmpPath := jarPath + ".tmp"

	// G304: jarPath is constructed from a config-supplied RuntimeDir (operator-
	// controlled) combined with a deterministic file name — not a path-traversal risk.
	tmp, err := os.Create(tmpPath) //nolint:gosec
	if err != nil {
		// Defensive path: reachable only on OS-level failure (permissions / fd exhaustion).
		return fmt.Errorf("suwayomi.EnsureJAR: create temp file: %w", err)
	}
	// Ensure the temp file is always cleaned up on error.
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			removeTmp(tmpPath)
		}
	}()

	if err := fetchAndWrite(ctx, url, tmp, timeout); err != nil {
		return err
	}

	if err := tmp.Sync(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		return fmt.Errorf("suwayomi.EnsureJAR: fsync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / corrupt FS).
		return fmt.Errorf("suwayomi.EnsureJAR: close temp file: %w", err)
	}

	// Atomic rename: a partial download NEVER becomes visible at the final path.
	if err := os.Rename(tmpPath, jarPath); err != nil {
		// Defensive path: reachable only on OS-level failure (cross-device rename / permissions).
		return fmt.Errorf("suwayomi.EnsureJAR: rename to final path: %w", err)
	}

	success = true
	return nil
}

// fetchAndWrite sends a GET request to url, verifies the response is 200 and
// non-empty, then streams the body into dst. timeout, when non-zero, is applied
// as an additional per-request deadline layered on top of ctx.
func fetchAndWrite(ctx context.Context, url string, dst *os.File, timeout time.Duration) error {
	reqCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// G107: url is constructed from cfg.DownloadURLTemplate which is operator-
	// supplied via config — not user-controlled input that could trigger SSRF.
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil) //nolint:gosec
	if err != nil {
		return fmt.Errorf("suwayomi.EnsureJAR: build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("suwayomi.EnsureJAR: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("suwayomi.EnsureJAR: unexpected status %d from %s", resp.StatusCode, url)
	}

	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		return fmt.Errorf("suwayomi.EnsureJAR: stream body: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("suwayomi.EnsureJAR: server returned empty body from %s", url)
	}

	return nil
}

// removeTmp silently removes a temporary file. Called on error paths where the
// primary error has already been captured; the cleanup error is intentionally
// discarded.
//
// Defensive path: reachable only on OS-level I/O failure; 0% coverage is
// expected per engineering standard.
func removeTmp(path string) {
	_ = os.Remove(path)
}
