package enginehost_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginehost"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// newExtLauncher builds a Launcher whose DataDir (the default instance's data
// root, where the shared extensions dir lives) is base. The client factory and
// process/health seams are never used by the extensions-link step.
func newExtLauncher(base string) *enginehost.Launcher {
	return enginehost.New(
		enginehost.EngineHostLauncherConfig{DataDir: base},
		func(string) sourceengine.Client { return nil },
	)
}

// TestLinkSharedExtensions_SymlinksProfileToBase proves the crux fix: a profile
// data dir's extensions/ is created as a SYMLINK to the default instance's
// extensions dir, so the profile inherits every installed extension instead of
// booting with an empty one. The base extensions dir is created if absent.
func TestLinkSharedExtensions_SymlinksProfileToBase(t *testing.T) {
	base := t.TempDir()
	profileDataDir := filepath.Join(base, "profiles", "abc123")

	l := newExtLauncher(base)
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err != nil {
		t.Fatalf("LinkSharedExtensions: %v", err)
	}

	baseExt := filepath.Join(base, "extensions")
	if fi, err := os.Stat(baseExt); err != nil || !fi.IsDir() {
		t.Fatalf("base extensions dir not present as a dir: fi=%v err=%v", fi, err)
	}

	link := filepath.Join(profileDataDir, "extensions")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected a symlink at %q: %v", link, err)
	}
	if target != baseExt {
		t.Errorf("symlink → %q, want %q", target, baseExt)
	}
}

// TestLinkSharedExtensions_InheritsBaseContents proves an extension installed on
// the DEFAULT instance is visible through a profile's symlinked extensions dir —
// the actual "profile has the sources" guarantee, not just "a link exists".
func TestLinkSharedExtensions_InheritsBaseContents(t *testing.T) {
	base := t.TempDir()
	baseExt := filepath.Join(base, "extensions")
	if err := os.MkdirAll(baseExt, 0o750); err != nil {
		t.Fatalf("mkdir baseExt: %v", err)
	}
	apk := filepath.Join(baseExt, "source.apk")
	if err := os.WriteFile(apk, []byte("dex"), 0o600); err != nil {
		t.Fatalf("write apk: %v", err)
	}

	profileDataDir := filepath.Join(base, "profiles", "abc123")
	l := newExtLauncher(base)
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err != nil {
		t.Fatalf("LinkSharedExtensions: %v", err)
	}

	// Read the apk THROUGH the profile's symlinked extensions dir.
	seen := filepath.Join(profileDataDir, "extensions", "source.apk")
	//nolint:gosec // G304: reading a test-controlled temp path.
	if got, err := os.ReadFile(seen); err != nil || string(got) != "dex" {
		t.Errorf("apk not visible through profile link: got=%q err=%v", got, err)
	}
}

// TestLinkSharedExtensions_Idempotent proves a second call with the correct
// symlink already in place is a no-op (no error, link unchanged).
func TestLinkSharedExtensions_Idempotent(t *testing.T) {
	base := t.TempDir()
	profileDataDir := filepath.Join(base, "profiles", "abc123")

	l := newExtLauncher(base)
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err != nil {
		t.Fatalf("first LinkSharedExtensions: %v", err)
	}
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err != nil {
		t.Fatalf("second LinkSharedExtensions: %v", err)
	}

	link := filepath.Join(profileDataDir, "extensions")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != filepath.Join(base, "extensions") {
		t.Errorf("symlink → %q, want %q", target, filepath.Join(base, "extensions"))
	}
}

// TestLinkSharedExtensions_ReplacesEmptyRealDir proves an EMPTY real extensions
// dir (e.g. one the engine-host pre-created on a prior boot before this link
// existed) is replaced by the symlink.
func TestLinkSharedExtensions_ReplacesEmptyRealDir(t *testing.T) {
	base := t.TempDir()
	profileDataDir := filepath.Join(base, "profiles", "abc123")
	realExt := filepath.Join(profileDataDir, "extensions")
	if err := os.MkdirAll(realExt, 0o750); err != nil {
		t.Fatalf("mkdir realExt: %v", err)
	}

	l := newExtLauncher(base)
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err != nil {
		t.Fatalf("LinkSharedExtensions: %v", err)
	}

	target, err := os.Readlink(realExt)
	if err != nil {
		t.Fatalf("expected the empty real dir to be replaced by a symlink: %v", err)
	}
	if target != filepath.Join(base, "extensions") {
		t.Errorf("symlink → %q, want %q", target, filepath.Join(base, "extensions"))
	}
}

// TestLinkSharedExtensions_ReplacesStaleSymlink proves a symlink pointing at the
// WRONG target is re-pointed at the base extensions dir.
func TestLinkSharedExtensions_ReplacesStaleSymlink(t *testing.T) {
	base := t.TempDir()
	profileDataDir := filepath.Join(base, "profiles", "abc123")
	if err := os.MkdirAll(profileDataDir, 0o750); err != nil {
		t.Fatalf("mkdir profileDataDir: %v", err)
	}
	link := filepath.Join(profileDataDir, "extensions")
	if err := os.Symlink(filepath.Join(base, "somewhere-else"), link); err != nil {
		t.Fatalf("pre-create stale symlink: %v", err)
	}

	l := newExtLauncher(base)
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err != nil {
		t.Fatalf("LinkSharedExtensions: %v", err)
	}

	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != filepath.Join(base, "extensions") {
		t.Errorf("symlink → %q, want %q", target, filepath.Join(base, "extensions"))
	}
}

// TestLinkSharedExtensions_NonEmptyRealDirErrors proves the non-destructive
// error-handling choice: a NON-empty real extensions dir (owner-installed APKs
// from a legacy non-symlinked run) is NOT silently deleted — the step returns an
// error (the spawn then degrades the profile to the default) and the data is
// left intact.
func TestLinkSharedExtensions_NonEmptyRealDirErrors(t *testing.T) {
	base := t.TempDir()
	profileDataDir := filepath.Join(base, "profiles", "abc123")
	realExt := filepath.Join(profileDataDir, "extensions")
	if err := os.MkdirAll(realExt, 0o750); err != nil {
		t.Fatalf("mkdir realExt: %v", err)
	}
	installed := filepath.Join(realExt, "installed.apk")
	if err := os.WriteFile(installed, []byte("dex"), 0o600); err != nil {
		t.Fatalf("write installed apk: %v", err)
	}

	l := newExtLauncher(base)
	if err := enginehost.LinkSharedExtensions(l, profileDataDir); err == nil {
		t.Fatalf("expected an error for a non-empty real extensions dir, got nil")
	}
	// The owner's data must be untouched.
	if _, err := os.Stat(installed); err != nil {
		t.Errorf("owner-installed apk was destroyed: %v", err)
	}
}
