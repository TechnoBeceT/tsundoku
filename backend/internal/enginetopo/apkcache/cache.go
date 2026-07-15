// Package apkcache is a small content-addressed, on-disk cache of extension
// .apk bytes. It exists so that Tsundoku can recover the engine's installed
// extensions entirely from its own store — re-installing the exact cached .apk
// — even when the upstream extension repository is offline.
//
// Each cached extension is a single file named "<pkg>-<version>.apk" under one
// directory, so re-caching the same (pkg, version) overwrites in place and two
// versions of the same extension can coexist. All writes are atomic (temp file
// → fsync → rename), mirroring internal/disk's writeFileAtomic idiom, so a
// crashed or failed write never leaves a partial file at the final path.
package apkcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotCached is returned by Open when no cached .apk exists for the requested
// (pkg, version). It is a distinct sentinel — never a raw os.IsNotExist error —
// so an HTTP caller maps "not cached" to a 404 without conflating it with a real
// I/O failure.
var ErrNotCached = errors.New("apkcache: extension apk not cached")

// Store is a content-addressed cache of extension .apk files living under a
// single directory. It holds no state beyond the directory path, so it is
// safe to share across goroutines (every method is a standalone filesystem op).
type Store struct {
	dir string
}

// New constructs a Store rooted at dir. It does no I/O — the directory is
// created lazily on the first Put — so constructing a Store never fails.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Path returns the absolute cache path a given (pkg, version) apk lives at,
// whether or not it currently exists. pkg is reduced to a single safe filename
// segment (see sanitizePkg) so a hostile package name can never point the path
// outside the cache directory.
func (s *Store) Path(pkg string, version int) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s-%d.apk", sanitizePkg(pkg), version))
}

// Put streams r into the cache at Path(pkg, version) and returns the SHA-256 hex
// of the bytes written plus the final path. The write is atomic: bytes go to a
// unique sibling temp file that is fsynced then renamed over the final path, so a
// mid-write failure — including a reader that errors part-way through — removes
// the temp file and never leaves a partial file at the final path (any previous
// cached file stays intact). Put is idempotent: a re-Put of the same
// (pkg, version) overwrites the file.
func (s *Store) Put(pkg string, version int, r io.Reader) (sha256Hex string, path string, err error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied).
		return "", "", fmt.Errorf("apkcache: create cache dir: %w", err)
	}
	finalPath := s.Path(pkg, version)

	// A UNIQUE temp file (os.CreateTemp fills the "*" with a random suffix) —
	// never a fixed "<final>.tmp" — so two concurrent Puts of the same
	// (pkg, version) write to separate temp files and can never interleave into
	// one and corrupt each other. The rename below stays the single atomic commit.
	f, err := os.CreateTemp(s.dir, filepath.Base(finalPath)+".*.tmp")
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / permission denied).
		return "", "", fmt.Errorf("apkcache: create temp file: %w", err)
	}
	tmpPath := f.Name()

	// Hash while writing so the caller gets the sha256 without a second read.
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), r); err != nil {
		// A failed source reader lands here: drop the temp file so no partial apk survives.
		_ = f.Close()
		removeQuietly(tmpPath)
		return "", "", fmt.Errorf("apkcache: write apk bytes: %w", err)
	}
	if err := f.Sync(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeQuietly(tmpPath)
		return "", "", fmt.Errorf("apkcache: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / corrupt FS).
		removeQuietly(tmpPath)
		return "", "", fmt.Errorf("apkcache: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (cross-device rename / permission).
		removeQuietly(tmpPath)
		return "", "", fmt.Errorf("apkcache: rename into place: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), finalPath, nil
}

// Open opens the cached .apk for (pkg, version) for reading. The caller owns the
// returned reader and must Close it. A missing file is reported as ErrNotCached
// (never a raw os.IsNotExist error); any other failure is surfaced as-is.
func (s *Store) Open(pkg string, version int) (io.ReadCloser, error) {
	// G304: the path is built from the cache root + a sanitised package name.
	//nolint:gosec
	f, err := os.Open(s.Path(pkg, version))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotCached
	}
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied).
		return nil, fmt.Errorf("apkcache: open cached apk: %w", err)
	}
	return f, nil
}

// Exists reports whether the cached .apk file for (pkg, version) is present on
// disk. It is the durable-truth check the seed uses: a HarvestedExtension row
// claiming apk_cached=true is trusted ONLY when the file actually backs it, so a
// recreated engine volume (Postgres survives, the bytes do not) triggers a
// re-download instead of a false skip that would 404 at recovery time.
func (s *Store) Exists(pkg string, version int) bool {
	_, err := os.Stat(s.Path(pkg, version))
	return err == nil
}

// Remove deletes the cached .apk for (pkg, version). A missing file is not an
// error — uninstalling an already-absent apk is a no-op — so Remove is safe to
// call unconditionally on an extension uninstall.
func (s *Store) Remove(pkg string, version int) error {
	if err := os.Remove(s.Path(pkg, version)); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Defensive path: reachable only on OS-level I/O failure (permission denied).
		return fmt.Errorf("apkcache: remove cached apk: %w", err)
	}
	return nil
}

// removeQuietly deletes a temp file on a write-failure cleanup path, discarding
// the error (there is nothing useful a caller can do about a failed cleanup of a
// temp file it was already abandoning).
func removeQuietly(path string) {
	_ = os.Remove(path)
}

// sanitizePkg reduces an extension package name to a single safe filename
// segment: every rune that is not an ASCII letter, digit, dot, dash, or
// underscore is replaced with an underscore. This strips path separators and any
// "../" traversal sequence, so a package name can never escape the cache
// directory. Real Android package names (e.g.
// "eu.kanade.tachiyomi.extension.en.mangadex") consist only of allowed runes and
// pass through unchanged. An empty result (a package name of only illegal runes)
// falls back to "_" so Path always yields a valid filename.
func sanitizePkg(pkg string) string {
	var b strings.Builder
	for _, r := range pkg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
