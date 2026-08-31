package enginetopo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
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

// TestSessionPolicyResolver_Matrix proves the selected global/endpoint session
// is resolved independently for Inherit, On, and Off. The resolver lets mutation
// preflight reject explicit On with no selected session before persistence
// rather than silently turning it into disposable behavior.
func TestSessionPolicyResolver_Matrix(t *testing.T) { //nolint:gocognit,cyclop // Table-driven policy matrix intentionally checks each independent result dimension.
	t.Parallel()

	policies := []struct {
		name     string
		override *bool
	}{
		{name: "inherit"},
		{name: "on", override: boolPointer(true)},
		{name: "off", override: boolPointer(false)},
	}
	modes := []string{
		network.FlareModeGlobal,
		network.FlareModeEndpoint,
		network.FlareModeNone,
	}
	sessions := []struct {
		name  string
		value string
	}{
		{name: "blank"},
		{name: "whitespace-is-configured", value: "  "},
		{name: "nonblank", value: "configured-session"},
	}

	for _, policy := range policies {
		for _, mode := range modes {
			for _, session := range sessions {
				name := policy.name + "/" + mode + "/" + session.name
				t.Run(name, func(t *testing.T) {
					base := baseConfig()
					base.fsSessionName = session.value
					binding := network.ResolvedBinding{SourceID: 71, FlareMode: mode}
					if mode == network.FlareModeEndpoint {
						binding.Flare = &network.ResolvedFlare{ID: "flare", Session: session.value}
					}
					resolver := enginetopo.NewSessionPolicyResolver(fakeSnapshotter{bindings: []network.ResolvedBinding{binding}}, base)

					reuse, gotMode, err := resolver.ResolveBypassSession(context.Background(), 71, policy.override)
					if mode == network.FlareModeNone {
						if err != nil || reuse || gotMode != sourcetransport.BypassSessionDisabled {
							t.Fatalf("ResolveBypassSession() = (%v, %q, %v), want disabled", reuse, gotMode, err)
						}
						return
					}
					if policy.name == "on" && session.value == "" {
						if err == nil || !strings.Contains(err.Error(), "nonblank") {
							t.Fatalf("ResolveBypassSession() error = %v, want nonblank-session rejection", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("ResolveBypassSession(): %v", err)
					}

					wantReuse := policy.name != "off" && session.value != ""
					wantMode := sourcetransport.BypassSessionDisposable
					if wantReuse {
						wantMode = sourcetransport.BypassSessionReusable
					}
					if reuse != wantReuse || gotMode != wantMode {
						t.Fatalf("ResolveBypassSession() = (%v, %q), want (%v, %q)", reuse, gotMode, wantReuse, wantMode)
					}
				})
			}
		}
	}
}

// TestProfileConfigProvider_DisposableSessionBlanksOnlySession proves Off
// propagates a blank session for both global and endpoint profiles while every
// unrelated FlareSolverr field keeps its selected value.
func TestProfileConfigProvider_DisposableSessionBlanksOnlySession(t *testing.T) { //nolint:cyclop // Configuration oracle asserts all fields remain unchanged except the session.
	t.Parallel()
	ctx := context.Background()

	base := baseConfig()
	global := enginetopo.NewProfileConfigProvider(engineroute.Profile{
		FlareMode: engineroute.FlareModeGlobal, DisableBypassSession: true,
	}, base)
	if got := global.FlareSolverrSessionName(ctx); got != "" {
		t.Fatalf("global Off session = %q, want blank", got)
	}
	if global.FlareSolverrURL(ctx) != base.fsURL || global.FlareSolverrTimeout(ctx) != base.fsTimeout ||
		global.FlareSolverrSessionTTL(ctx) != base.fsSessionTTL || global.FlareSolverrResponseFallback(ctx) != base.fsFallback {
		t.Fatalf("global Off changed unrelated config: url=%q timeout=%d ttl=%d fallback=%v",
			global.FlareSolverrURL(ctx), global.FlareSolverrTimeout(ctx), global.FlareSolverrSessionTTL(ctx), global.FlareSolverrResponseFallback(ctx))
	}

	endpointValue := &engineroute.FlareEndpoint{
		ID: "flare", URL: "http://endpoint.test:8191", Session: "endpoint-session",
		SessionTTL: 31, Timeout: 47, AsResponseFallback: true,
	}
	endpoint := enginetopo.NewProfileConfigProvider(engineroute.Profile{
		FlareMode: engineroute.FlareModeEndpoint, Flare: endpointValue, DisableBypassSession: true,
	}, base)
	if got := endpoint.FlareSolverrSessionName(ctx); got != "" {
		t.Fatalf("endpoint Off session = %q, want blank", got)
	}
	if endpoint.FlareSolverrURL(ctx) != endpointValue.URL || endpoint.FlareSolverrTimeout(ctx) != endpointValue.Timeout ||
		endpoint.FlareSolverrSessionTTL(ctx) != endpointValue.SessionTTL || endpoint.FlareSolverrResponseFallback(ctx) != endpointValue.AsResponseFallback {
		t.Fatalf("endpoint Off changed unrelated config: url=%q timeout=%d ttl=%d fallback=%v",
			endpoint.FlareSolverrURL(ctx), endpoint.FlareSolverrTimeout(ctx), endpoint.FlareSolverrSessionTTL(ctx), endpoint.FlareSolverrResponseFallback(ctx))
	}
}

func boolPointer(value bool) *bool { return &value }
