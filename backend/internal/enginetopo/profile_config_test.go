package enginetopo_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/enginetopo"
)

// TestProfileConfigProvider_ResponseFallbackFromEndpoint proves the per-profile
// FlareSolverr response-fallback flag comes from the BOUND ENDPOINT in "endpoint"
// mode (so a per-endpoint toggle actually reaches the instance), overriding the
// base global value. The base fixture has fsFallback=false, so an endpoint that
// says true proves the value is read from the endpoint, not the base.
func TestProfileConfigProvider_ResponseFallbackFromEndpoint(t *testing.T) {
	ctx := context.Background()
	base := baseConfig() // fsFallback: false

	p := engineroute.Profile{
		FlareMode: engineroute.FlareModeEndpoint,
		Flare:     &engineroute.FlareEndpoint{URL: "http://flare.test:8191", AsResponseFallback: true},
	}
	cp := enginetopo.NewProfileConfigProvider(p, base)
	if !cp.FlareSolverrResponseFallback(ctx) {
		t.Error("endpoint mode: FlareSolverrResponseFallback = false, want the endpoint's true (not base false)")
	}

	// And the inverse: an endpoint that opts OUT is honoured even if base is on.
	base.fsFallback = true
	pOff := engineroute.Profile{
		FlareMode: engineroute.FlareModeEndpoint,
		Flare:     &engineroute.FlareEndpoint{URL: "http://flare.test:8191", AsResponseFallback: false},
	}
	if enginetopo.NewProfileConfigProvider(pOff, base).FlareSolverrResponseFallback(ctx) {
		t.Error("endpoint mode: FlareSolverrResponseFallback = true, want the endpoint's false (not base true)")
	}
}

// TestProfileConfigProvider_ResponseFallbackInheritsBase proves the non-endpoint
// flare modes (global/none) inherit the base global response-fallback flag —
// they have no bound endpoint to read it from, so the global default stands.
func TestProfileConfigProvider_ResponseFallbackInheritsBase(t *testing.T) {
	ctx := context.Background()
	base := baseConfig()
	base.fsFallback = true

	for _, mode := range []string{engineroute.FlareModeGlobal, engineroute.FlareModeNone} {
		p := engineroute.Profile{FlareMode: mode}
		if !enginetopo.NewProfileConfigProvider(p, base).FlareSolverrResponseFallback(ctx) {
			t.Errorf("%s mode: FlareSolverrResponseFallback = false, want the inherited base true", mode)
		}
	}
}
