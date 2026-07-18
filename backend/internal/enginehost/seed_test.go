package enginehost_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginehost"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// newSeedLauncherReal builds a Launcher for the seed tests. Only KCEF seeding is
// exercised, so the ClientFactory is a never-invoked stub and the process/health
// seams keep their production defaults (also never used here).
func newSeedLauncherReal(kcefBundle string) *enginehost.Launcher {
	return enginehost.New(
		enginehost.EngineHostLauncherConfig{KCEFBundle: kcefBundle},
		func(string) sourceengine.Client { return nil },
	)
}

// TestSeedKCEF_LinksBundleAndClearsLocks proves seeding symlinks the bundle into
// "<dataDir>/bin/kcef" and removes a stale Chromium singleton lock.
func TestSeedKCEF_LinksBundleAndClearsLocks(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "kcef-bundle")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	dataDir := filepath.Join(root, "profile-data")
	// Pre-create a stale singleton lock that seeding must clear.
	lockDir := filepath.Join(dataDir, "cache", "kcef")
	if err := os.MkdirAll(lockDir, 0o750); err != nil {
		t.Fatalf("mkdir lockDir: %v", err)
	}
	stale := filepath.Join(lockDir, "SingletonLock")
	if err := os.WriteFile(stale, []byte("dead-host"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	l := newSeedLauncherReal(bundle)
	enginehost.SeedKCEF(l, dataDir)

	link := filepath.Join(dataDir, "bin", "kcef")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %q: %v", link, err)
	}
	if target != bundle {
		t.Errorf("symlink → %q, want %q", target, bundle)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale SingletonLock not cleared (stat err=%v)", err)
	}
}

// TestSeedKCEF_Idempotent proves a second seed with the link already in place is
// a no-op (no error, link unchanged).
func TestSeedKCEF_Idempotent(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "kcef-bundle")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	dataDir := filepath.Join(root, "profile-data")

	l := newSeedLauncherReal(bundle)
	enginehost.SeedKCEF(l, dataDir)
	enginehost.SeedKCEF(l, dataDir) // second pass

	target, err := os.Readlink(filepath.Join(dataDir, "bin", "kcef"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != bundle {
		t.Errorf("symlink → %q, want %q", target, bundle)
	}
}

// TestSeedKCEF_MissingBundleSkips proves a blank/absent bundle seeds nothing and
// never errors (best-effort — a dev box without the baked Chromium still boots).
func TestSeedKCEF_MissingBundleSkips(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "profile-data")

	// Absent bundle path.
	l := newSeedLauncherReal(filepath.Join(root, "does-not-exist"))
	enginehost.SeedKCEF(l, dataDir)
	if _, err := os.Stat(filepath.Join(dataDir, "bin", "kcef")); !os.IsNotExist(err) {
		t.Errorf("bin/kcef was created for a missing bundle (err=%v)", err)
	}

	// Blank bundle.
	l2 := newSeedLauncherReal("")
	enginehost.SeedKCEF(l2, dataDir)
	if _, err := os.Stat(filepath.Join(dataDir, "bin", "kcef")); !os.IsNotExist(err) {
		t.Errorf("bin/kcef was created for a blank bundle (err=%v)", err)
	}
}
