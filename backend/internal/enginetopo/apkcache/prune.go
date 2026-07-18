package apkcache

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CachedVersion is one HELD (retained) extension .apk version — the durable
// record backing the reversible-update feature. The set of a package's
// CachedVersions is what the Extensions UI lists so the owner can pick an older
// build to reinstall (e.g. roll back an update that broke a source's parser),
// without hunting through raw .apk files on disk. It is stored on the
// HarvestedExtension row (cached_versions JSON), one entry per .apk still on
// disk after a prune.
type CachedVersion struct {
	// VersionCode is the extension's numeric version — the (pkg, VersionCode)
	// key the cached "<pkg>-<VersionCode>.apk" file lives at and the reinstall
	// endpoint addresses.
	VersionCode int `json:"versionCode"`
	// VersionName is the human-readable version string for display.
	VersionName string `json:"versionName"`
	// CachedAt is when these bytes were first cached.
	CachedAt time.Time `json:"cachedAt"`
}

// ListVersions returns the version codes of every cached "<pkg>-<version>.apk"
// file currently on disk for pkg, in no particular order. A missing cache
// directory is not an error (nothing cached yet ⇒ empty slice). It reads the
// directory and prefix-matches on the sanitised package name rather than using
// filepath.Glob, so a package name containing a glob metacharacter can never be
// mis-parsed (the same discipline internal/disk.removeStaleCovers follows); the
// version segment must be a pure integer, so a sibling package whose sanitised
// name is a prefix of pkg (e.g. "a" vs "a-b") never leaks its files into pkg's
// list — its trailing "-b-<n>" fails the integer parse and is skipped.
func (s *Store) ListVersions(pkg string) ([]int, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied).
		return nil, fmt.Errorf("apkcache: read cache dir: %w", err)
	}
	prefix := sanitizePkg(pkg) + "-"
	var versions []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		mid, ok := strings.CutSuffix(e.Name(), ".apk")
		if !ok {
			continue
		}
		verStr, ok := strings.CutPrefix(mid, prefix)
		if !ok {
			continue
		}
		v, err := strconv.Atoi(verStr)
		if err != nil {
			continue // not "<pkg>-<int>.apk" (e.g. a sibling package's file)
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// Prune deletes cached .apk files for pkg beyond the newest keepNewest versions,
// ALWAYS keeping every version in keepAlso even if it falls outside the newest N.
// It returns the retained version codes, newest-first.
//
// keepAlso is the load-bearing guard: the caller passes the currently-INSTALLED
// version code so a prune can NEVER delete the running extension's .apk — the
// retained set is (newest keepNewest) ∪ keepAlso. keepNewest < 1 is clamped to 1
// (keep at least the current build). A missing cache directory prunes nothing.
func (s *Store) Prune(pkg string, keepNewest int, keepAlso ...int) ([]int, error) {
	if keepNewest < 1 {
		keepNewest = 1
	}
	all, err := s.ListVersions(pkg)
	if err != nil {
		return nil, err
	}
	keep := make(map[int]bool, len(keepAlso))
	for _, v := range keepAlso {
		keep[v] = true
	}
	retain, remove := selectRetained(all, keepNewest, keep)
	for _, v := range remove {
		if err := s.Remove(pkg, v); err != nil {
			// Defensive path: reachable only on OS-level I/O failure.
			return nil, err
		}
	}
	return retain, nil
}

// selectRetained is the PURE partition at the heart of Prune (unit-tested in
// isolation): given every cached version code, the number of newest versions to
// keep, and an explicit keep-set (the installed version), it returns the version
// codes to KEEP (newest-first) and the ones to REMOVE. A version is kept when it
// is among the newest keepNewest OR present in keepAlso, so the installed
// version survives even when older than the newest N.
func selectRetained(all []int, keepNewest int, keepAlso map[int]bool) (retain []int, remove []int) {
	sorted := append([]int(nil), all...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	for i, v := range sorted {
		if i < keepNewest || keepAlso[v] {
			retain = append(retain, v)
		} else {
			remove = append(remove, v)
		}
	}
	return retain, remove
}
