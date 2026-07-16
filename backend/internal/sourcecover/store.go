// Package sourcecover restores the disk-cache-first behaviour Suwayomi's own
// ImageResponse.kt (getImageResponse) had for source-manga thumbnails, which
// the P2 Suwayomi-removal engine swap dropped for the two engine-fed cover
// proxies (Discover/Search source covers + the metadata-source picker's
// per-provider covers): both used to burst-refetch every visible cover LIVE
// on every grid render (SourceCalls.image() on the engine host is a
// cacheless HTTP fetch — no Cache-Control anywhere in the chain), which trips
// Cloudflare's per-source rate-limiting on a protected source and, because
// each slow challenge holds a same-origin connection to the backend, can
// saturate the browser's per-host connection cap and hang the whole SPA.
//
// GAP-085 restores the cache — Go-side, not engine-side (a deliberate choice:
// Suwayomi cached in the JVM process; Tsundoku already has its own Go-side
// disk cover cache for LIBRARY series covers, series.Service.CoverBytes —
// see internal/series/cover.go — so this mirrors that established pattern
// instead of introducing a second caching strategy) — plus a fail-fast
// deadline + bounded concurrency around the cold-miss engine fetch (see
// cache.go), because the cache alone cannot help a cover that has never been
// fetched before.
//
// Store is the disk half: a content-addressed, INDEFINITE cache keyed by
// (sourceID, url). No TTL/expiry is needed, and that is a deliberate design
// choice, not an oversight: every source URL Tsundoku is handed already
// carries its own cache-buster when the image changes (observed live, e.g.
// "theblank.net/covers/...?v=1784205310"), so a changed cover is a CHANGED
// URL — a new cache key — and therefore self-invalidates. Keying by the full
// URL (not just a manga/chapter id) makes that invalidation free.
package sourcecover

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Store is a content-addressed cache of source-cover image bytes living
// under a single directory. It holds no state beyond the directory path, so
// it is safe to share across goroutines (every method is a standalone
// filesystem op) — mirrors internal/enginetopo/apkcache.Store's shape.
type Store struct {
	dir string
}

// New constructs a Store rooted at dir. It does no I/O — the directory is
// created lazily on the first Put — so constructing a Store never fails.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// key derives the cache filename for (sourceID, url): a sha256 hex digest of
// the pair, NUL-separated so no url could ever collide two different
// sourceIDs onto the same key. Hashing (rather than embedding the raw url in
// the filename) sidesteps every filesystem-unsafe-character and path-length
// concern a real source URL could otherwise raise.
func key(sourceID int64, url string) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(sourceID, 10) + "\x00" + url))
	return hex.EncodeToString(sum[:])
}

// path returns the absolute cache path a given (sourceID, url) entry lives
// at, whether or not it currently exists.
func (s *Store) path(sourceID int64, url string) string {
	return filepath.Join(s.dir, key(sourceID, url)+".bin")
}

// Get reads the cached entry for (sourceID, url). ok is false on ANY read or
// decode failure (missing file, truncated/corrupt entry) — a Store never
// distinguishes "never cached" from "cache entry unusable"; both are a MISS
// to the caller, which re-fetches and overwrites via Put.
func (s *Store) Get(sourceID int64, url string) (data []byte, ext string, ok bool) {
	// G304: path is built from the cache root + a sha256 hex digest, never a
	// caller-controlled path segment.
	//nolint:gosec
	raw, err := os.ReadFile(s.path(sourceID, url))
	if err != nil {
		return nil, "", false
	}
	return decodeEntry(raw)
}

// Put stores data + its ext (the raw string engine.Image returned — see
// cache.go's Engine port; NOT necessarily a bare file extension, whatever it
// is is round-tripped verbatim so a warm serve resolves the exact same
// Content-Type a cold serve would) under (sourceID, url). The write is
// atomic: a UNIQUE temp file (os.CreateTemp's "*" gets a random suffix, never
// a fixed "<final>.tmp" — so two concurrent Puts of the same key can never
// interleave into one file and corrupt each other) is fsynced then renamed
// over the final path, mirroring apkcache.Store.Put's proven shape and
// Suwayomi's own ImageResponse.kt saveImage (write to a temp path, rename
// into place; a file still named ".tmp" after an interrupted write is never
// mistaken for a valid cache entry, because Get only ever reads the FINAL
// ".bin" name). A mid-write failure leaves no partial file at the final path.
func (s *Store) Put(sourceID int64, url string, data []byte, ext string) error {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied).
		return fmt.Errorf("sourcecover: create cache dir: %w", err)
	}
	finalPath := s.path(sourceID, url)

	f, err := os.CreateTemp(s.dir, filepath.Base(finalPath)+".*.tmp")
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / permission denied).
		return fmt.Errorf("sourcecover: create temp file: %w", err)
	}
	tmpPath := f.Name()

	payload, err := encodeEntry(data, ext)
	if err != nil {
		_ = f.Close()
		removeQuietly(tmpPath)
		return err
	}
	if _, err := f.Write(payload); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		_ = f.Close()
		removeQuietly(tmpPath)
		return fmt.Errorf("sourcecover: write cache entry: %w", err)
	}
	if err := f.Sync(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeQuietly(tmpPath)
		return fmt.Errorf("sourcecover: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / corrupt FS).
		removeQuietly(tmpPath)
		return fmt.Errorf("sourcecover: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (cross-device rename / permission).
		removeQuietly(tmpPath)
		return fmt.Errorf("sourcecover: rename into place: %w", err)
	}
	return nil
}

// removeQuietly deletes a temp file on a write-failure cleanup path,
// discarding the error (there is nothing useful a caller can do about a
// failed cleanup of a temp file it was already abandoning).
func removeQuietly(path string) {
	_ = os.Remove(path)
}

// maxExtLen is the largest ext (an HTTP Content-Type header value in
// practice) encodeEntry's 2-byte length prefix can address. Real values are a
// handful of bytes ("image/jpeg"); this bound exists only to make the
// uint16 conversion below provably safe, never to be reached in practice.
const maxExtLen = 1<<16 - 1

// encodeEntry packs data + ext into one cache-file payload: a 2-byte
// big-endian length prefix for ext, then ext's bytes, then data verbatim. A
// single length-prefixed file (rather than a second sidecar file for the
// ext/content-type) keeps the atomic write to ONE temp-file-then-rename, so
// there is no window where a bytes file exists without its ext (or vice
// versa) for Get to misread as a valid-but-mismatched entry.
func encodeEntry(data []byte, ext string) ([]byte, error) {
	extBytes := []byte(ext)
	if len(extBytes) > maxExtLen {
		// Defensive path: no real Content-Type value comes anywhere close;
		// guards the uint16 conversion below rather than ever firing live.
		return nil, fmt.Errorf("sourcecover: ext too long to cache (%d bytes)", len(extBytes))
	}
	buf := make([]byte, 2+len(extBytes)+len(data))
	// G115: the maxExtLen check above proves len(extBytes) <= 65535, so this
	// conversion cannot overflow — gosec can't see across the guard.
	//nolint:gosec
	binary.BigEndian.PutUint16(buf[:2], uint16(len(extBytes)))
	copy(buf[2:], extBytes)
	copy(buf[2+len(extBytes):], data)
	return buf, nil
}

// decodeEntry is encodeEntry's inverse. ok is false for any payload too
// short to hold its own length prefix, or whose declared ext length overruns
// the buffer — both indicate a truncated/corrupt file, never a valid cache
// hit.
func decodeEntry(raw []byte) (data []byte, ext string, ok bool) {
	if len(raw) < 2 {
		return nil, "", false
	}
	n := int(binary.BigEndian.Uint16(raw[:2]))
	if len(raw) < 2+n {
		return nil, "", false
	}
	return raw[2+n:], string(raw[2 : 2+n]), true
}
