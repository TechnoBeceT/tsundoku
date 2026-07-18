package apkcache_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
)

const prunePkg = "eu.kanade.tachiyomi.extension.en.test"

// putVersion caches a trivial .apk for (pkg, version) in the store.
func putVersion(t *testing.T, s *apkcache.Store, pkg string, version int) {
	t.Helper()
	if _, _, err := s.Put(pkg, version, strings.NewReader("apk-bytes")); err != nil {
		t.Fatalf("Put v%d: %v", version, err)
	}
}

// TestListVersions_ParsesCachedFiles proves ListVersions returns exactly the
// cached version codes for a package and ignores unrelated files.
func TestListVersions_ParsesCachedFiles(t *testing.T) {
	s := apkcache.New(t.TempDir())
	for _, v := range []int{3, 41, 42} {
		putVersion(t, s, prunePkg, v)
	}
	// A sibling package whose bytes must never leak into prunePkg's list.
	putVersion(t, s, prunePkg+".other", 99)

	got, err := s.ListVersions(prunePkg)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	sort.Ints(got)
	if want := []int{3, 41, 42}; !equalInts(got, want) {
		t.Fatalf("ListVersions = %v, want %v", got, want)
	}
}

// TestListVersions_MissingDirIsEmpty proves an absent cache directory is not an
// error (nothing cached yet).
func TestListVersions_MissingDirIsEmpty(t *testing.T) {
	s := apkcache.New(t.TempDir() + "/never-created")
	got, err := s.ListVersions(prunePkg)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListVersions = %v, want empty", got)
	}
}

// TestPrune_KeepsNewestNAndDeletesRest proves Prune keeps the newest N versions'
// .apk files and deletes the older ones from disk.
func TestPrune_KeepsNewestNAndDeletesRest(t *testing.T) {
	s := apkcache.New(t.TempDir())
	for _, v := range []int{10, 20, 30, 40} {
		putVersion(t, s, prunePkg, v)
	}
	retain, err := s.Prune(prunePkg, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := []int{40, 30}; !equalInts(retain, want) {
		t.Fatalf("retained = %v, want %v (newest-first)", retain, want)
	}
	assertExists(t, s, 40, true)
	assertExists(t, s, 30, true)
	assertExists(t, s, 20, false)
	assertExists(t, s, 10, false)
}

// TestPrune_AlwaysKeepsInstalledEvenIfOlderThanN proves the installed version's
// .apk survives a prune even when it is older than the newest N — the rollback
// invariant that the running build's bytes are never evicted.
func TestPrune_AlwaysKeepsInstalledEvenIfOlderThanN(t *testing.T) {
	s := apkcache.New(t.TempDir())
	for _, v := range []int{10, 20, 30, 40} {
		putVersion(t, s, prunePkg, v)
	}
	// keepNewest=2 → {40,30}; installed=10 must ALSO survive.
	retain, err := s.Prune(prunePkg, 2, 10)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(retain)))
	if want := []int{40, 30, 10}; !equalInts(retain, want) {
		t.Fatalf("retained = %v, want %v (installed 10 pinned)", retain, want)
	}
	assertExists(t, s, 10, true)
	assertExists(t, s, 20, false)
}

// TestPrune_NoOpWhenWithinN proves a package with no more than N versions is
// left entirely intact.
func TestPrune_NoOpWhenWithinN(t *testing.T) {
	s := apkcache.New(t.TempDir())
	putVersion(t, s, prunePkg, 5)
	putVersion(t, s, prunePkg, 7)
	retain, err := s.Prune(prunePkg, 3)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := []int{7, 5}; !equalInts(retain, want) {
		t.Fatalf("retained = %v, want %v", retain, want)
	}
	assertExists(t, s, 5, true)
	assertExists(t, s, 7, true)
}

func assertExists(t *testing.T, s *apkcache.Store, version int, want bool) {
	t.Helper()
	if got := s.Exists(prunePkg, version); got != want {
		t.Errorf("Exists(v%d) = %v, want %v", version, got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
