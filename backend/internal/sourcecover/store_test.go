// Package sourcecover_test black-box exercises the content-addressed on-disk
// cover cache: round-trip, key derivation (distinct sourceID/url never
// collide; the SAME url auto-invalidates a stale entry because it becomes a
// NEW key), idempotent re-put, and a miss on an unusable entry. No network,
// no DB — every test uses a t.TempDir() Store, mirroring
// internal/enginetopo/apkcache's test shape (the established pattern for
// this exact kind of content-addressed disk cache in this codebase).
package sourcecover_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourcecover"
)

// TestStore_PutGetRoundTrip proves the happy path: Put then Get returns the
// exact bytes and ext back.
func TestStore_PutGetRoundTrip(t *testing.T) {
	store := sourcecover.New(t.TempDir())
	data := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01}

	if err := store.Put(7, "https://source.example/cover.png?v=1", data, "png"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	gotData, gotExt, ok := store.Get(7, "https://source.example/cover.png?v=1")
	if !ok {
		t.Fatal("Get: want hit, got miss")
	}
	if string(gotData) != string(data) {
		t.Errorf("Get: data = %v, want %v", gotData, data)
	}
	if gotExt != "png" {
		t.Errorf("Get: ext = %q, want %q", gotExt, "png")
	}
}

// TestStore_MissReturnsFalse proves an un-cached (sourceID, url) is a clean
// miss, never an error.
func TestStore_MissReturnsFalse(t *testing.T) {
	store := sourcecover.New(t.TempDir())
	_, _, ok := store.Get(1, "https://source.example/never-cached.jpg")
	if ok {
		t.Fatal("Get: want miss for an un-cached entry, got hit")
	}
}

// TestStore_DifferentSourceIDsDoNotCollide proves two different sourceIDs
// requesting the SAME url address independent cache entries — the key is
// (sourceID, url), never url alone.
func TestStore_DifferentSourceIDsDoNotCollide(t *testing.T) {
	store := sourcecover.New(t.TempDir())
	const url = "https://source.example/shared-path/cover.jpg"

	if err := store.Put(1, url, []byte("source-one-bytes"), "jpg"); err != nil {
		t.Fatalf("Put source 1: %v", err)
	}
	if err := store.Put(2, url, []byte("source-two-bytes"), "jpg"); err != nil {
		t.Fatalf("Put source 2: %v", err)
	}

	got1, _, ok := store.Get(1, url)
	if !ok || string(got1) != "source-one-bytes" {
		t.Errorf("Get source 1: data = %q, ok = %v, want %q, true", got1, ok, "source-one-bytes")
	}
	got2, _, ok := store.Get(2, url)
	if !ok || string(got2) != "source-two-bytes" {
		t.Errorf("Get source 2: data = %q, ok = %v, want %q, true", got2, ok, "source-two-bytes")
	}
}

// TestStore_ChangedURLIsANewKey is the load-bearing proof behind the
// package's "no TTL needed" design: a cover whose URL changed (the source
// republished it with a new cache-buster, e.g. "?v=...") is cached under a
// DIFFERENT key from the old URL — both entries independently exist; there is
// no explicit invalidation step, because the OLD key is simply never asked
// for again once the DB/candidate holds the new URL.
func TestStore_ChangedURLIsANewKey(t *testing.T) {
	store := sourcecover.New(t.TempDir())
	const sourceID = 5

	if err := store.Put(sourceID, "https://source.example/cover.jpg?v=1", []byte("old-bytes"), "jpg"); err != nil {
		t.Fatalf("Put old: %v", err)
	}
	if err := store.Put(sourceID, "https://source.example/cover.jpg?v=2", []byte("new-bytes"), "jpg"); err != nil {
		t.Fatalf("Put new: %v", err)
	}

	oldData, _, ok := store.Get(sourceID, "https://source.example/cover.jpg?v=1")
	if !ok || string(oldData) != "old-bytes" {
		t.Errorf("Get old url: data = %q, ok = %v, want %q, true", oldData, ok, "old-bytes")
	}
	newData, _, ok := store.Get(sourceID, "https://source.example/cover.jpg?v=2")
	if !ok || string(newData) != "new-bytes" {
		t.Errorf("Get new url: data = %q, ok = %v, want %q, true", newData, ok, "new-bytes")
	}
}

// TestStore_PutIdempotentReput proves a re-Put of the same (sourceID, url)
// overwrites in place: the second content is what Get reads back, and no
// duplicate file is left behind.
func TestStore_PutIdempotentReput(t *testing.T) {
	dir := t.TempDir()
	store := sourcecover.New(dir)
	const sourceID = 3
	const url = "https://source.example/re.jpg"

	if err := store.Put(sourceID, url, []byte("v1"), "jpg"); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := store.Put(sourceID, url, []byte("v2-longer-content"), "jpg"); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	got, _, ok := store.Get(sourceID, url)
	if !ok || string(got) != "v2-longer-content" {
		t.Errorf("Get after re-put: data = %q, ok = %v, want %q, true", got, ok, "v2-longer-content")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("cache dir has %d files after re-put, want 1: %v", len(entries), names)
	}
}

// TestStore_PutFailureLeavesNoPartialFile proves atomicity: if the cache
// directory cannot even be created (here: a plain FILE already occupies that
// path, a portable and deterministic way to force MkdirAll to fail), Put
// reports the error and leaves nothing behind for a later Get to
// misinterpret as a valid — or as a corrupt-but-present — entry.
func TestStore_PutFailureLeavesNoPartialFile(t *testing.T) {
	parent := t.TempDir()
	blocked := filepath.Join(parent, "blocked")
	if err := os.WriteFile(blocked, []byte("i am a file, not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	store := sourcecover.New(blocked)

	if err := store.Put(1, "https://source.example/x.jpg", []byte("data"), "jpg"); err == nil {
		t.Fatal("Put: want error when the cache dir path is blocked by a file, got nil")
	}
	if _, _, ok := store.Get(1, "https://source.example/x.jpg"); ok {
		t.Error("Get: want miss after a failed Put, got hit")
	}
}
