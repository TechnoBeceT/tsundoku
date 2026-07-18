package enginehost_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

// TestFsSafeKey_DeterministicAndSafe proves the profile-key hash is stable, is a
// 16-char lowercase hex string, and contains none of the awkward characters a
// raw profile key carries ("|", UUIDs) that must never reach a filesystem path.
func TestFsSafeKey_DeterministicAndSafe(t *testing.T) {
	key := "3f1c-uuid-socks|endpoint|9a2b-uuid-flare"

	got := enginehost.FsSafeKey(key)
	if !hex16.MatchString(got) {
		t.Fatalf("FsSafeKey = %q, want 16 hex chars", got)
	}
	if again := enginehost.FsSafeKey(key); again != got {
		t.Errorf("FsSafeKey not deterministic: %q then %q", got, again)
	}
	for _, bad := range []string{"|", "/", " ", ":"} {
		if strings.Contains(got, bad) {
			t.Errorf("FsSafeKey %q contains unsafe %q", got, bad)
		}
	}
}

// TestFsSafeKey_DistinctKeysDistinctDirs proves two different profiles never
// collide on a data dir.
func TestFsSafeKey_DistinctKeysDistinctDirs(t *testing.T) {
	if enginehost.FsSafeKey("a|global|") == enginehost.FsSafeKey("b|global|") {
		t.Error("distinct profile keys hashed to the same data-dir name")
	}
}

// TestDataDirFor_PathShape proves a profile's data dir is "<base>/profiles/<hash>".
func TestDataDirFor_PathShape(t *testing.T) {
	got := enginehost.DataDirFor("/config/engine", "k1")
	want := filepath.Join("/config/engine", "profiles", enginehost.FsSafeKey("k1"))
	if got != want {
		t.Fatalf("DataDirFor = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "/config/engine/profiles/") {
		t.Errorf("DataDirFor = %q, want it under <base>/profiles/", got)
	}
}
