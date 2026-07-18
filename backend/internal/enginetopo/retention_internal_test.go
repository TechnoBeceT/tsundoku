package enginetopo

import (
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
)

// TestBuildCachedVersions_MergesNamesAndStampsNew proves the pure held-set merge:
// retained versions (newest-first) are emitted reusing the existing stored names
// + cachedAt, the just-cached newVersion is (re)stamped with its name, and a
// retained version with no prior record is emitted with an empty name.
func TestBuildCachedVersions_MergesNamesAndStampsNew(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	existing := []apkcache.CachedVersion{
		{VersionCode: 41, VersionName: "1.4.1", CachedAt: t0},
		{VersionCode: 40, VersionName: "1.4.0", CachedAt: t0},
	}

	// Retained set = {42 (new), 41 (existing), 39 (retained but unknown name)}.
	got := buildCachedVersions(existing, []int{42, 41, 39}, 42, "1.4.2", now)

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].VersionCode != 42 || got[0].VersionName != "1.4.2" || !got[0].CachedAt.Equal(now) {
		t.Errorf("new version entry = %+v, want {42 1.4.2 now}", got[0])
	}
	// Existing 41 keeps its stored name AND original cachedAt.
	if got[1].VersionCode != 41 || got[1].VersionName != "1.4.1" || !got[1].CachedAt.Equal(t0) {
		t.Errorf("existing entry = %+v, want {41 1.4.1 t0}", got[1])
	}
	// 39 has no prior record → empty name, current time.
	if got[2].VersionCode != 39 || got[2].VersionName != "" {
		t.Errorf("unknown entry = %+v, want {39 \"\" now}", got[2])
	}
}

// TestBuildCachedVersions_PreservesNewVersionNameWhenBlank proves that re-recording
// an already-known version with a blank name keeps the previously stored name (and
// its original cache time) rather than blanking it — the reinstall path passes ""
// on purpose.
func TestBuildCachedVersions_PreservesNewVersionNameWhenBlank(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	existing := []apkcache.CachedVersion{{VersionCode: 41, VersionName: "1.4.1", CachedAt: t0}}

	got := buildCachedVersions(existing, []int{41}, 41, "", now)

	if len(got) != 1 || got[0].VersionName != "1.4.1" || !got[0].CachedAt.Equal(t0) {
		t.Fatalf("entry = %+v, want name 1.4.1 + original cachedAt preserved", got[0])
	}
}
