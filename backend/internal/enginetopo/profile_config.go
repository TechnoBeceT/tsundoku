package enginetopo

import (
	"context"

	"github.com/technobecet/tsundoku/internal/engineroute"
)

// socksDefaultPort is the SOCKS port a profile with no SOCKS override pushes
// while disabled — a harmless placeholder (Enabled is false, so it is never
// dialed), matching the settings overlay's own EngineSocksPort default.
const socksDefaultPort = 1080

// profileConfigProvider adapts one engineroute.Profile to the ConfigProvider
// surface Reconcile pushes onto an engine-host instance, so the profile's OWN
// FlareSolverr + SOCKS config (not the global default) lands on ITS instance:
//   - FlareSolverr: "none" ⇒ disabled; "global" ⇒ inherit the base global config;
//     "endpoint" ⇒ the bound FlareSolverr endpoint's config.
//   - SOCKS: no override ⇒ disabled; a bound SOCKS endpoint ⇒ its host/port/
//     version (credentials are pushed separately — see pushSocksCredentials —
//     because ConfigProvider's surface can't express them).
//
// It satisfies ConfigProvider so reconcileConfig pushes it verbatim; base is only
// consulted for the "global" flare mode (and for the FlareSolverr response-
// fallback flag, which has no per-profile counterpart).
type profileConfigProvider struct {
	profile engineroute.Profile
	base    ConfigProvider
}

// Compile-time assertion.
var _ ConfigProvider = profileConfigProvider{}

// FlareSolverrEnabled reports whether this profile solves Cloudflare challenges:
// true for "global" (when the base global config is on) or "endpoint"; false for
// "none".
func (p profileConfigProvider) FlareSolverrEnabled(ctx context.Context) bool {
	switch p.profile.FlareMode {
	case engineroute.FlareModeEndpoint:
		return true
	case engineroute.FlareModeGlobal:
		return p.base.FlareSolverrEnabled(ctx)
	default: // none
		return false
	}
}

// FlareSolverrURL returns the endpoint's URL for "endpoint" mode, the base global
// URL for "global", and "" for "none".
func (p profileConfigProvider) FlareSolverrURL(ctx context.Context) string {
	switch p.profile.FlareMode {
	case engineroute.FlareModeEndpoint:
		return p.profile.Flare.URL
	case engineroute.FlareModeGlobal:
		return p.base.FlareSolverrURL(ctx)
	default:
		return ""
	}
}

// FlareSolverrTimeout returns the endpoint's timeout for "endpoint" mode, else
// the base global timeout (a harmless value when disabled).
func (p profileConfigProvider) FlareSolverrTimeout(ctx context.Context) int {
	if p.profile.FlareMode == engineroute.FlareModeEndpoint {
		return p.profile.Flare.Timeout
	}
	return p.base.FlareSolverrTimeout(ctx)
}

// FlareSolverrSessionName returns the endpoint's session for "endpoint" mode,
// the base global session for "global", and "" for "none".
func (p profileConfigProvider) FlareSolverrSessionName(ctx context.Context) string {
	switch p.profile.FlareMode {
	case engineroute.FlareModeEndpoint:
		return p.profile.Flare.Session
	case engineroute.FlareModeGlobal:
		return p.base.FlareSolverrSessionName(ctx)
	default:
		return ""
	}
}

// FlareSolverrSessionTTL returns the endpoint's session TTL for "endpoint" mode,
// else the base global TTL.
func (p profileConfigProvider) FlareSolverrSessionTTL(ctx context.Context) int {
	if p.profile.FlareMode == engineroute.FlareModeEndpoint {
		return p.profile.Flare.SessionTTL
	}
	return p.base.FlareSolverrSessionTTL(ctx)
}

// FlareSolverrResponseFallback inherits the base global flag — there is no
// per-profile counterpart in the binding model.
func (p profileConfigProvider) FlareSolverrResponseFallback(ctx context.Context) bool {
	return p.base.FlareSolverrResponseFallback(ctx)
}

// EngineSocksEnabled reports whether this profile routes through a SOCKS proxy —
// true iff it has a bound SOCKS endpoint.
func (p profileConfigProvider) EngineSocksEnabled(context.Context) bool {
	return p.profile.Socks != nil
}

// EngineSocksHost returns the bound SOCKS endpoint's host, or "" when there is
// none.
func (p profileConfigProvider) EngineSocksHost(context.Context) string {
	if p.profile.Socks == nil {
		return ""
	}
	return p.profile.Socks.Host
}

// EngineSocksPort returns the bound SOCKS endpoint's port, or the disabled
// placeholder default when there is none.
func (p profileConfigProvider) EngineSocksPort(context.Context) int {
	if p.profile.Socks == nil {
		return socksDefaultPort
	}
	return p.profile.Socks.Port
}

// EngineSocksVersion returns the bound SOCKS endpoint's version, or SOCKS5 when
// there is none.
func (p profileConfigProvider) EngineSocksVersion(context.Context) int {
	if p.profile.Socks == nil {
		return 5
	}
	return p.profile.Socks.Version
}
