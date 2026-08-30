package enginetopo

import (
	"context"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/settings"
)

// socksDefaultPort is the SOCKS port a profile with no SOCKS override pushes
// while disabled — a harmless placeholder (Enabled is false, so it is never
// dialed), matching the settings overlay's own EngineSocksPort default.
const socksDefaultPort = 1080

// profileConfigProvider adapts one engineroute.Profile to the ConfigProvider
// surface Reconcile pushes onto an engine-host instance, so the profile's OWN
// FlareSolverr + SOCKS config (not the global default) lands on ITS instance:
//   - FlareSolverr: "none" ⇒ disabled; "global" ⇒ inherit the base global config;
//     "endpoint" ⇒ the bound FlareSolverr endpoint's config; disposable-session
//     policy blanks only the selected global/endpoint session.
//   - SOCKS: no override ⇒ disabled; a bound SOCKS endpoint ⇒ its host/port/
//     version (credentials are pushed separately — see pushSocksCredentials —
//     because ConfigProvider's surface can't express them).
//
// It satisfies ConfigProvider so reconcileConfig pushes it verbatim; base is
// consulted for the "global" flare mode (and for the FlareSolverr response-
// fallback flag in the non-"endpoint" modes, which have no bound endpoint to
// read it from).
type profileConfigProvider struct {
	profile engineroute.Profile
	base    ConfigProvider
}

type imageTransportSourceProvider interface {
	ImageTransportSources(context.Context) []int64
}

type frozenConfig struct {
	flareEnabled       bool
	flareURL           string
	flareTimeout       int
	flareSessionName   string
	flareSessionTTL    int
	flareFallback      bool
	socksEnabled       bool
	socksHost          string
	socksPort          int
	socksVersion       int
	impersonateEnabled bool
	impersonateURL     string
	impersonateSources []int64
	imageSources       []int64
}

type runtimeConfigSnapshotter interface {
	RuntimeConfigSnapshot(context.Context) (settings.RuntimeConfigSnapshot, error)
}

func freezeConfig(ctx context.Context, cfg ConfigProvider, imageSources []int64) (frozenConfig, error) {
	if snapshotter, ok := cfg.(runtimeConfigSnapshotter); ok {
		snapshot, err := snapshotter.RuntimeConfigSnapshot(ctx)
		if err != nil {
			return frozenConfig{}, err
		}
		return frozenConfig{
			flareEnabled:       snapshot.FlareSolverrEnabled,
			flareURL:           snapshot.FlareSolverrURL,
			flareTimeout:       snapshot.FlareSolverrTimeout,
			flareSessionName:   snapshot.FlareSolverrSessionName,
			flareSessionTTL:    snapshot.FlareSolverrSessionTTL,
			flareFallback:      snapshot.FlareSolverrResponseFallback,
			socksEnabled:       snapshot.EngineSocksEnabled,
			socksHost:          snapshot.EngineSocksHost,
			socksPort:          snapshot.EngineSocksPort,
			socksVersion:       snapshot.EngineSocksVersion,
			impersonateEnabled: snapshot.ImpersonateEnabled,
			impersonateURL:     snapshot.ImpersonateURL,
			impersonateSources: append([]int64(nil), snapshot.ImpersonateSources...),
			imageSources:       append([]int64(nil), imageSources...),
		}, nil
	}
	return frozenConfig{
		flareEnabled:       cfg.FlareSolverrEnabled(ctx),
		flareURL:           cfg.FlareSolverrURL(ctx),
		flareTimeout:       cfg.FlareSolverrTimeout(ctx),
		flareSessionName:   cfg.FlareSolverrSessionName(ctx),
		flareSessionTTL:    cfg.FlareSolverrSessionTTL(ctx),
		flareFallback:      cfg.FlareSolverrResponseFallback(ctx),
		socksEnabled:       cfg.EngineSocksEnabled(ctx),
		socksHost:          cfg.EngineSocksHost(ctx),
		socksPort:          cfg.EngineSocksPort(ctx),
		socksVersion:       cfg.EngineSocksVersion(ctx),
		impersonateEnabled: cfg.ImpersonateEnabled(ctx),
		impersonateURL:     cfg.ImpersonateURL(ctx),
		impersonateSources: append([]int64(nil), cfg.ImpersonateSources(ctx)...),
		imageSources:       append([]int64(nil), imageSources...),
	}, nil
}

func (c frozenConfig) FlareSolverrEnabled(context.Context) bool          { return c.flareEnabled }
func (c frozenConfig) FlareSolverrURL(context.Context) string            { return c.flareURL }
func (c frozenConfig) FlareSolverrTimeout(context.Context) int           { return c.flareTimeout }
func (c frozenConfig) FlareSolverrSessionName(context.Context) string    { return c.flareSessionName }
func (c frozenConfig) FlareSolverrSessionTTL(context.Context) int        { return c.flareSessionTTL }
func (c frozenConfig) FlareSolverrResponseFallback(context.Context) bool { return c.flareFallback }
func (c frozenConfig) EngineSocksEnabled(context.Context) bool           { return c.socksEnabled }
func (c frozenConfig) EngineSocksHost(context.Context) string            { return c.socksHost }
func (c frozenConfig) EngineSocksPort(context.Context) int               { return c.socksPort }
func (c frozenConfig) EngineSocksVersion(context.Context) int            { return c.socksVersion }
func (c frozenConfig) ImpersonateEnabled(context.Context) bool           { return c.impersonateEnabled }
func (c frozenConfig) ImpersonateURL(context.Context) string             { return c.impersonateURL }
func (c frozenConfig) ImpersonateSources(context.Context) []int64 {
	return append([]int64(nil), c.impersonateSources...)
}
func (c frozenConfig) ImageTransportSources(context.Context) []int64 {
	return append([]int64(nil), c.imageSources...)
}

func imageTransportSources(ctx context.Context, cfg ConfigProvider) []int64 {
	provider, ok := cfg.(imageTransportSourceProvider)
	if !ok {
		return []int64{}
	}
	ids := provider.ImageTransportSources(ctx)
	if ids == nil {
		return []int64{}
	}
	return ids
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

// FlareSolverrSessionName returns "" when this profile has disposable-session
// policy, otherwise the endpoint's session for "endpoint" mode, the base global
// session for "global", and "" for "none".
func (p profileConfigProvider) FlareSolverrSessionName(ctx context.Context) string {
	if p.profile.DisableBypassSession {
		return ""
	}
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

// FlareSolverrResponseFallback returns the bound endpoint's reactive-fallback
// flag for "endpoint" mode (so a per-endpoint toggle actually reaches the
// instance), and inherits the base global flag for "global"/"none" (which have
// no per-profile FlareSolverr endpoint to read it from).
func (p profileConfigProvider) FlareSolverrResponseFallback(ctx context.Context) bool {
	if p.profile.FlareMode == engineroute.FlareModeEndpoint {
		return p.profile.Flare.AsResponseFallback
	}
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

// ImpersonateEnabled inherits the base global impersonate-gateway toggle: the
// Chrome-fingerprint image gateway (GAP-111) is a single global service, not a
// per-profile network endpoint, so every profile instance uses the same value.
func (p profileConfigProvider) ImpersonateEnabled(ctx context.Context) bool {
	return p.base.ImpersonateEnabled(ctx)
}

// ImpersonateURL inherits the base global impersonate-gateway URL (see
// ImpersonateEnabled — one shared gateway for every profile).
func (p profileConfigProvider) ImpersonateURL(ctx context.Context) string {
	return p.base.ImpersonateURL(ctx)
}

// ImpersonateSources inherits the base per-source gating set (GAP-131). Which
// sources need a browser TLS fingerprint is a property of the SOURCE's CDN, not
// of the network profile the source egresses through, so every profile instance
// receives the same set — and a source routed to a profile instance is gated
// there exactly as it would be on the default instance.
func (p profileConfigProvider) ImpersonateSources(ctx context.Context) []int64 {
	return p.base.ImpersonateSources(ctx)
}

// ImageTransportSources forwards the full replace-set carried by the base
// runtime snapshot. The set is global by source ID, so every profile instance
// must receive the same value regardless of which sources it currently routes.
func (p profileConfigProvider) ImageTransportSources(ctx context.Context) []int64 {
	return imageTransportSources(ctx, p.base)
}
