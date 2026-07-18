package enginetopo

import "github.com/technobecet/tsundoku/internal/engineroute"

// export_test.go exposes unexported internals to the black-box enginetopo_test
// package. Compiled only under `go test`.

// NewProfileConfigProvider exposes profileConfigProvider so the per-profile
// ConfigProvider mapping (especially the FlareSolverr response-fallback source
// selection) can be unit-tested directly.
func NewProfileConfigProvider(p engineroute.Profile, base ConfigProvider) ConfigProvider {
	return profileConfigProvider{profile: p, base: base}
}
