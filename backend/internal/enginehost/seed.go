package enginehost

import (
	"log/slog"
	"os"
	"path/filepath"
)

// seedKCEF prepares a spawned instance's data dir so its embedded Chromium
// (KCEF) starts with no first-run download and no stale singleton lock —
// mirroring what entrypoint.sh does for the default instance (see the "Seed the
// BUNDLED KCEF runtime" block there). It:
//   - symlinks "<dataDir>/bin/kcef" → the baked-in bundle (idempotent: skipped
//     when the link already points there), so CEFManager finds Chromium locally;
//   - removes any stale "<dataDir>/cache/kcef/Singleton{Lock,Cookie,Socket}"
//     left by a previously-killed instance (a dead hostname in the lock makes
//     Chromium refuse to launch, and every WebView source would then time out).
//
// It is BEST-EFFORT BY DESIGN: every step logs and continues on failure, and the
// whole function is a no-op when KCEFBundle is blank or missing. A KCEF-seeding
// failure only degrades WebView-gated sources on this one instance — it must
// never fail the spawn (which would degrade the profile to the default instance
// for ALL its sources). This is the sole sanctioned "log-and-continue" path in
// the package; every other error is returned up the stack.
func (l *Launcher) seedKCEF(dataDir string) {
	l.linkKCEFBundle(dataDir)
	clearKCEFSingletonLocks(dataDir)
}

// linkKCEFBundle creates the "<dataDir>/bin/kcef" symlink to the baked bundle,
// unless the bundle is unset/absent or the link already points there.
func (l *Launcher) linkKCEFBundle(dataDir string) {
	bundle := l.cfg.KCEFBundle
	if bundle == "" {
		return
	}
	if _, err := os.Stat(bundle); err != nil {
		// A missing bundle is expected on a dev box without the baked Chromium —
		// not worth a warning, just skip.
		return
	}

	binDir := filepath.Join(dataDir, "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		slog.Warn("enginehost: could not create KCEF bin dir; skipping bundle link", "dir", binDir, "err", err)
		return
	}

	link := filepath.Join(binDir, "kcef")
	if existing, err := os.Readlink(link); err == nil && existing == bundle {
		return // already linked to the right place — idempotent
	}
	// Replace any stale link/file so the symlink lands cleanly (ln -sfn parity).
	_ = os.Remove(link)
	if err := os.Symlink(bundle, link); err != nil {
		slog.Warn("enginehost: could not symlink KCEF bundle; WebView sources may be slow", "link", link, "bundle", bundle, "err", err)
	}
}

// clearKCEFSingletonLocks removes the Chromium singleton lock/cookie/socket files
// a previously-killed instance may have left behind. Missing files are fine
// (os.Remove's not-exist error is ignored).
func clearKCEFSingletonLocks(dataDir string) {
	cacheDir := filepath.Join(dataDir, "cache", "kcef")
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(cacheDir, name))
	}
}
