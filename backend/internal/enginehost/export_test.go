package enginehost

import "time"

// export_test.go exposes unexported helpers to the black-box enginehost_test
// package so pure/internal logic can be pinned directly without spawning a real
// process. It is compiled only under `go test`.

// DataDirFor exposes dataDirFor.
func DataDirFor(base, key string) string { return dataDirFor(base, key) }

// FsSafeKey exposes fsSafeKey.
func FsSafeKey(key string) string { return fsSafeKey(key) }

// BuildHostEnv exposes buildHostEnv so the per-instance env shape is testable
// without exec.
func BuildHostEnv(base []string, port int, dataDir string, disableKCEF bool) []string {
	return buildHostEnv(base, port, dataDir, disableKCEF)
}

// SeedKCEF exposes the (best-effort) KCEF-seeding step so it can be driven
// against a temp dir + fake bundle without a spawn.
func SeedKCEF(l *Launcher, dataDir string) { l.seedKCEF(dataDir) }

// LinkSharedExtensions exposes the (fail-loud) shared-extensions symlink step so
// it can be driven against temp dirs without a spawn.
func LinkSharedExtensions(l *Launcher, profileDataDir string) error {
	return l.linkSharedExtensions(profileDataDir)
}

// HTTPHealthProber exposes the production HTTP prober constructor.
func HTTPHealthProber(timeout time.Duration) HealthProber { return newHTTPHealthProber(timeout) }

// AllocFreePort exposes the production free-port allocator.
func AllocFreePort() (int, error) { return allocFreePort() }
