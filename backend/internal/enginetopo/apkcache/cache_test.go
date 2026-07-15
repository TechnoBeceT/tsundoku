// Package apkcache_test black-box exercises the content-addressed on-disk .apk
// cache: round-trip, sha256 stability, atomicity on a mid-write failure,
// idempotent re-put, Remove, and the ErrNotCached sentinel. No network, no DB —
// every test uses a t.TempDir() Store.
package apkcache_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
)

// sha256Hex is the expected hex digest of data, computed independently of the
// Store so a test never grades the Store against its own arithmetic.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// failingReader yields `good` bytes and then a hard error, simulating a source
// (e.g. an HTTP body) that dies part-way through a download.
type failingReader struct {
	good []byte
	sent bool
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if !f.sent {
		f.sent = true
		n := copy(p, f.good)
		return n, nil
	}
	return 0, f.err
}

// TestPutOpenRoundTrip proves the happy path: Put returns the correct sha256 +
// path, and Open reads back the exact bytes.
func TestPutOpenRoundTrip(t *testing.T) {
	store := apkcache.New(t.TempDir())
	data := []byte("fake apk bytes \x00\x01\x02 payload")

	gotSHA, gotPath, err := store.Put("eu.kanade.tachiyomi.extension.en.mangadex", 42, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if want := sha256Hex(data); gotSHA != want {
		t.Errorf("Put sha256 = %q, want %q", gotSHA, want)
	}
	if gotPath != store.Path("eu.kanade.tachiyomi.extension.en.mangadex", 42) {
		t.Errorf("Put path = %q, want %q", gotPath, store.Path("eu.kanade.tachiyomi.extension.en.mangadex", 42))
	}

	rc, err := store.Open("eu.kanade.tachiyomi.extension.en.mangadex", 42)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Open bytes = %q, want %q", got, data)
	}
}

// TestPutSHA256Stable proves the same bytes always hash to the same digest
// across two independent Puts (different pkg/version) — the digest is a pure
// function of the content.
func TestPutSHA256Stable(t *testing.T) {
	store := apkcache.New(t.TempDir())
	data := []byte("stable content")

	a, _, err := store.Put("pkg.a", 1, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Put a: %v", err)
	}
	b, _, err := store.Put("pkg.b", 2, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if a != b {
		t.Errorf("sha256 differs for identical content: %q vs %q", a, b)
	}
	if a != sha256Hex(data) {
		t.Errorf("sha256 = %q, want %q", a, sha256Hex(data))
	}
}

// TestPutAtomicNoPartialOnReaderError proves atomicity: a reader that fails
// mid-stream leaves NO file at the final path and NO leftover .tmp file.
func TestPutAtomicNoPartialOnReaderError(t *testing.T) {
	dir := t.TempDir()
	store := apkcache.New(dir)

	fr := &failingReader{good: []byte("partial"), err: errors.New("boom")}
	_, _, err := store.Put("pkg.fail", 7, fr)
	if err == nil {
		t.Fatal("Put: want error from failing reader, got nil")
	}

	if _, statErr := os.Stat(store.Path("pkg.fail", 7)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("final path exists after failed Put (want absent): stat err = %v", statErr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cache dir not empty after failed Put: %v", dirNames(entries))
	}
}

// TestPutIdempotentReput proves a re-Put of the same (pkg, version) overwrites
// in place: the second content is what Open reads back, and no duplicate file
// is created.
func TestPutIdempotentReput(t *testing.T) {
	dir := t.TempDir()
	store := apkcache.New(dir)

	if _, _, err := store.Put("pkg.re", 3, bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if _, _, err := store.Put("pkg.re", 3, bytes.NewReader([]byte("second-version"))); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	rc, err := store.Open("pkg.re", 3)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "second-version" {
		t.Errorf("Open bytes = %q, want %q", got, "second-version")
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("cache dir has %d files after re-put, want 1: %v", len(entries), dirNames(entries))
	}
}

// TestOpenAbsentReturnsErrNotCached proves Open of an uncached apk returns the
// ErrNotCached sentinel (not a raw os error), so callers map it to a 404.
func TestOpenAbsentReturnsErrNotCached(t *testing.T) {
	store := apkcache.New(t.TempDir())
	_, err := store.Open("pkg.missing", 99)
	if !errors.Is(err, apkcache.ErrNotCached) {
		t.Errorf("Open absent: err = %v, want ErrNotCached", err)
	}
}

// TestRemove proves Remove deletes a cached apk and is a no-op (nil error) when
// the apk is already absent.
func TestRemove(t *testing.T) {
	store := apkcache.New(t.TempDir())

	if _, _, err := store.Put("pkg.rm", 5, bytes.NewReader([]byte("bytes"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Remove("pkg.rm", 5); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := store.Open("pkg.rm", 5); !errors.Is(err, apkcache.ErrNotCached) {
		t.Errorf("Open after Remove: err = %v, want ErrNotCached", err)
	}

	// Second Remove of an absent apk is a no-op.
	if err := store.Remove("pkg.rm", 5); err != nil {
		t.Errorf("Remove of absent apk: err = %v, want nil", err)
	}
}

// TestPathSanitisesPackageName proves a traversal-laden package name is confined
// to a single filename segment inside the cache directory.
func TestPathSanitisesPackageName(t *testing.T) {
	dir := t.TempDir()
	store := apkcache.New(dir)

	got := store.Path("../../etc/passwd", 1)
	wantDir := filepath.Clean(dir)
	if gotDir := filepath.Dir(got); gotDir != wantDir {
		t.Errorf("Path escaped cache dir: parent = %q, want %q (full = %q)", gotDir, wantDir, got)
	}
	if filepath.Base(got) == "passwd-1.apk" {
		t.Errorf("Path retained a traversal segment: %q", got)
	}
}

// dirNames is a tiny helper turning DirEntry slices into names for failure msgs.
func dirNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}
