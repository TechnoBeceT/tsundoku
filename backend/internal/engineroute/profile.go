// Package engineroute implements the QCAT-284 MULTI-INSTANCE per-source network
// routing mechanism: each source's engine RPC is routed to the engine-host
// instance matching its network profile, by running one engine-host per distinct
// network PROFILE.
//
// A PROFILE is a distinct {socks-endpoint?, flare-mode(none|global|endpoint) +
// flare-endpoint?, disposable-bypass-session?} combination. Every source that
// shares the same profile is served by the same engine-host instance; a source
// with no binding (or a binding that is equivalent to today's global config)
// uses the DEFAULT instance — the already-shipped single engine-host,
// byte-for-byte unchanged.
//
// This package owns three concerns, each in its own file:
//   - profile.go: the pure profile-derivation kernel (Derive) — no engine, no DB.
//   - router.go: the Router, a sourceengine.Client that dispatches each RPC to a
//     source's profile instance (or the default) by sourceId.
//   - launcher.go: the Launcher port (how a profile instance is brought up) plus
//     the conservative default-only production launcher.
//
// The DB→engine reconcile that wires it all together (read bindings → derive
// profiles → ensure instances → push each its config → build the routing map)
// lives in internal/enginetopo (ReconcileNetwork), the same home as the existing
// engine-topology reconcile — see QCAT-284 and spec/per-source-network-routing.
package engineroute

import (
	"sort"
	"strings"
)

// FlareMode values mirror internal/network's binding flare-mode vocabulary. They
// are duplicated here (rather than imported) so this pure engine package never
// depends on the DB-truth network domain — enginetopo maps between the two.
const (
	FlareModeNone     = "none"
	FlareModeGlobal   = "global"
	FlareModeEndpoint = "endpoint"
)

// SocksEndpoint is a resolved SOCKS-proxy egress an instance routes through. It
// carries the password DELIBERATELY (unlike the HTTP DTO): it is only ever used
// to push the instance's own SOCKS config, never serialized to a client.
type SocksEndpoint struct {
	// ID is the NetworkEndpoint's UUID string — the stable identity a profile
	// key is built from (an edit to the endpoint's host keeps the same ID, so
	// the same instance is reused and simply re-pushed the new config).
	ID       string
	Host     string
	Port     int
	Version  int
	Username string
	Password string
}

// FlareEndpoint is a resolved FlareSolverr egress an instance solves Cloudflare
// challenges through (used only when a profile's FlareMode is "endpoint").
type FlareEndpoint struct {
	// ID is the NetworkEndpoint's UUID string — see SocksEndpoint.ID.
	ID         string
	URL        string
	Session    string
	SessionTTL int
	Timeout    int
	// AsResponseFallback mirrors FlareSolverr's asResponseFallback flag: when
	// true the profile's instance uses FlareSolverr only reactively (as a
	// fallback for a blocked request), not for every request. It is pushed to
	// the instance verbatim for "endpoint" flare mode (see
	// enginetopo.profileConfigProvider).
	AsResponseFallback bool
}

// BindingInput is one source's resolved network binding — the Derive input. It
// is the engine-facing projection of a network.SourceNetworkBinding with its
// referenced endpoints already resolved (a disabled/absent dimension arrives as
// a nil pointer / global mode, so Derive needs no further filtering).
type BindingInput struct {
	SourceID             int64
	Socks                *SocksEndpoint // nil = direct (no SOCKS override)
	FlareMode            string         // none|global|endpoint ("" is treated as global)
	Flare                *FlareEndpoint // non-nil iff FlareMode == endpoint
	DisableBypassSession bool           // true = push a blank disposable session when bypass can be used
	// KCEFEnabled is the route-resolved embedded-browser capability for this
	// source. It is already normalized by the policy owner before derivation.
	KCEFEnabled bool
}

// Profile is one distinct network profile that needs its own engine-host
// instance. It is produced by Derive and consumed by ReconcileNetwork (to ensure
// the instance + push its config) and, via the routing map, by the Router.
type Profile struct {
	// Key is the canonical, stable identity of this profile (see profileKey). The
	// empty string is reserved for the DEFAULT profile and never appears here —
	// Derive only ever returns non-default profiles.
	Key string
	// Socks is this profile's SOCKS egress (nil = direct).
	Socks *SocksEndpoint
	// FlareMode is this profile's FlareSolverr scope (none|global|endpoint).
	FlareMode string
	// Flare is this profile's FlareSolverr endpoint (non-nil iff FlareMode ==
	// endpoint).
	Flare *FlareEndpoint
	// DisableBypassSession makes this profile push a blank FlareSolverr session.
	// It is normalized false for flare mode none, where bypass cannot be used.
	DisableBypassSession bool
	// KCEFEnabled is the explicit embedded-browser setting for this managed
	// instance. Launching code must consume this value rather than infer it from
	// the FlareSolverr route.
	KCEFEnabled bool
	// SourceIDs are every source bound to this profile, ascending. Two sources
	// with the same profile share one instance.
	SourceIDs []int64
}

// Derive groups a set of resolved source bindings into the distinct NON-DEFAULT
// profiles they require, each carrying the source ids that map to it.
//
// A binding is DEFAULT-equivalent — and therefore contributes NO profile (its
// source keeps using the default instance) — when its network/session route is
// default-equivalent AND its effective KCEF setting equals defaultKCEFEnabled.
// That keeps a capability divergence managed even when its route is otherwise
// global. Existing non-default route keys remain byte-identical when their
// KCEF setting matches that route's historical behavior; only a divergence
// from that behavior adds a capability discriminator.
//
// The result is deterministic: profiles are ordered by Key and each profile's
// SourceIDs are ascending, so the same input always yields the same routing map
// (a reconcile that changes nothing pushes nothing — idempotency).
func Derive(defaultKCEFEnabled bool, bindings []BindingInput) []Profile {
	byKey := make(map[string]*Profile)
	for _, b := range bindings {
		key := profileKey(b, defaultKCEFEnabled)
		if key == "" {
			continue // default-equivalent — routes to the default instance
		}
		p, ok := byKey[key]
		if !ok {
			mode := normalizeFlareMode(b.FlareMode)
			p = &Profile{
				Key:                  key,
				Socks:                b.Socks,
				FlareMode:            mode,
				Flare:                b.Flare,
				DisableBypassSession: bypassCanBeUsed(mode) && b.DisableBypassSession,
				KCEFEnabled:          b.KCEFEnabled,
			}
			byKey[key] = p
		}
		p.SourceIDs = append(p.SourceIDs, b.SourceID)
	}

	profiles := make([]Profile, 0, len(byKey))
	for _, p := range byKey {
		sort.Slice(p.SourceIDs, func(i, j int) bool { return p.SourceIDs[i] < p.SourceIDs[j] })
		profiles = append(profiles, *p)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Key < profiles[j].Key })
	return profiles
}

// profileKey is the canonical identity of a binding's profile — the empty string
// for a default-equivalent binding, else a stable composite of the SOCKS
// endpoint id, normalized flare mode, flare endpoint id, and only when needed a
// KCEF discriminator. Keying on endpoint IDs (not resolved field values) means
// an owner editing an endpoint's host/port/session keeps the SAME profile
// instance. The default-host comparison decides whether an otherwise-global
// route needs management; the historical-route comparison decides whether an
// already-managed route needs a KCEF discriminator, preserving legacy keys.
func profileKey(b BindingInput, defaultKCEFEnabled bool) string {
	socksID := socksEndpointID(b.Socks)
	mode := normalizeFlareMode(b.FlareMode)
	disableSession := bypassCanBeUsed(mode) && b.DisableBypassSession
	networkDefault := socksID == "" && mode == FlareModeGlobal && !disableSession
	if networkDefault && b.KCEFEnabled == defaultKCEFEnabled {
		return "" // no SOCKS override + global flare == today's default
	}
	if networkDefault {
		return kcefKey(b.KCEFEnabled)
	}
	flareID := flareEndpointID(b.Flare)
	key := strings.Join([]string{socksID, mode, flareID}, "|")
	if disableSession {
		key += "|session=disposable"
	}
	if b.KCEFEnabled != historicalKCEFEnabled(mode) {
		key += "|" + kcefKey(b.KCEFEnabled)
	}
	return key
}

func socksEndpointID(endpoint *SocksEndpoint) string {
	if endpoint == nil {
		return ""
	}
	return endpoint.ID
}

func flareEndpointID(endpoint *FlareEndpoint) string {
	if endpoint == nil {
		return ""
	}
	return endpoint.ID
}

// historicalKCEFEnabled is the behavior profile keys represented before KCEF
// became an explicit capability: endpoint bypass profiles ran without WebView;
// global and none profiles ran with it.
func historicalKCEFEnabled(flareMode string) bool {
	return flareMode != FlareModeEndpoint
}

func kcefKey(enabled bool) string {
	if enabled {
		return "kcef=on"
	}
	return "kcef=off"
}

// bypassCanBeUsed reports whether mode can issue FlareSolverr requests whose
// session choice matters.
func bypassCanBeUsed(mode string) bool {
	return mode == FlareModeGlobal || mode == FlareModeEndpoint
}

// normalizeFlareMode maps a blank/unknown flare mode onto the global default so
// the "" and "global" spellings are one profile, matching the binding schema's
// own default.
func normalizeFlareMode(mode string) string {
	switch mode {
	case FlareModeNone, FlareModeEndpoint:
		return mode
	default:
		return FlareModeGlobal
	}
}

// RoutingMap is a source-id → profile-key routing table (built by
// ReconcileNetwork from the derived profiles and handed to the Router). A source
// id absent from the map uses the default instance.
type RoutingMap map[int64]string
