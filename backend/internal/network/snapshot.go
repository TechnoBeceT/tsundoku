package network

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entbinding "github.com/technobecet/tsundoku/internal/ent/sourcenetworkbinding"
)

// ResolvedSocks is a binding's SOCKS endpoint with every field resolved,
// INCLUDING the password. Unlike EndpointDTO (which omits the write-only
// password) this is the engine-PUSH projection: it is only ever consumed by the
// per-profile config reconcile (enginetopo.ReconcileNetwork → SetSocks), never
// serialized to an HTTP client.
type ResolvedSocks struct {
	ID       string
	Host     string
	Port     int
	Version  int
	Username string
	Password string
}

// ResolvedFlare is a binding's FlareSolverr endpoint with every field resolved —
// the engine-push projection used when a binding's flare mode is "endpoint".
type ResolvedFlare struct {
	ID         string
	URL        string
	Proxy      string
	Session    string
	SessionTTL int
	Timeout    int
}

// ResolvedBinding is one source's binding with its referenced endpoints resolved
// to their full (secret-bearing) config — the input the engine-side profile
// derivation consumes. A dimension the owner left off, pointed at a missing
// endpoint, or pointed at a DISABLED endpoint arrives here already collapsed to
// its default (Socks nil / FlareMode "global"), so the consumer needs no further
// filtering.
type ResolvedBinding struct {
	SourceID  int64
	Socks     *ResolvedSocks // nil = direct (no SOCKS override)
	FlareMode string         // none|global|endpoint (a disabled/missing endpoint downgrades to global)
	Flare     *ResolvedFlare // non-nil iff FlareMode == endpoint
}

// RoutingSnapshot loads every per-source binding with its referenced endpoints
// resolved to their full config (SOCKS password included) — the authoritative
// input for engine-side profile derivation + per-profile config push. It is a
// SEPARATE read surface from ListBindings (which returns password-free DTOs for
// the HTTP layer): this one is for the internal engine-push consumer ONLY.
//
// An endpoint that is missing (referential drift) or DISABLED is treated as
// absent: a SOCKS reference resolves to nil (direct), and a "endpoint" flare mode
// whose endpoint is missing/disabled downgrades to "global". This keeps a
// half-configured or temporarily-disabled endpoint from silently routing a
// source through a broken egress — it just falls back to the default.
func (s *Service) RoutingSnapshot(ctx context.Context) ([]ResolvedBinding, error) {
	endpoints, err := s.client.NetworkEndpoint.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("network.RoutingSnapshot: query endpoints: %w", err)
	}
	byID := make(map[uuid.UUID]*ent.NetworkEndpoint, len(endpoints))
	for _, e := range endpoints {
		byID[e.ID] = e
	}

	bindings, err := s.client.SourceNetworkBinding.Query().
		Order(ent.Asc(entbinding.FieldSourceID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("network.RoutingSnapshot: query bindings: %w", err)
	}

	out := make([]ResolvedBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, resolveBinding(b, byID))
	}
	return out, nil
}

// resolveBinding turns one stored binding into a ResolvedBinding, resolving the
// SOCKS and FlareSolverr endpoint references against byID and collapsing every
// missing/disabled/wrong-kind reference to its default.
func resolveBinding(b *ent.SourceNetworkBinding, byID map[uuid.UUID]*ent.NetworkEndpoint) ResolvedBinding {
	rb := ResolvedBinding{SourceID: b.SourceID, FlareMode: b.FlareMode}
	rb.Socks = resolveSocks(b.SocksEndpointID, byID)
	if b.FlareMode == FlareModeEndpoint {
		if flare := resolveFlare(b.FlareEndpointID, byID); flare != nil {
			rb.Flare = flare
		} else {
			// Endpoint mode but the endpoint is missing/disabled/wrong-kind —
			// downgrade to the global default rather than route through nothing.
			rb.FlareMode = FlareModeGlobal
		}
	}
	return rb
}

// resolveSocks resolves an optional SOCKS endpoint reference to its full config,
// returning nil (direct) when the reference is absent, missing, disabled, or not
// a SOCKS endpoint.
func resolveSocks(id *uuid.UUID, byID map[uuid.UUID]*ent.NetworkEndpoint) *ResolvedSocks {
	row := usableEndpoint(id, byID, KindSocks)
	if row == nil {
		return nil
	}
	return &ResolvedSocks{
		ID:       row.ID.String(),
		Host:     row.Host,
		Port:     row.Port,
		Version:  row.SocksVersion,
		Username: row.Username,
		Password: row.Password,
	}
}

// resolveFlare resolves an optional FlareSolverr endpoint reference to its full
// config, returning nil when the reference is absent, missing, disabled, or not
// a FlareSolverr endpoint.
func resolveFlare(id *uuid.UUID, byID map[uuid.UUID]*ent.NetworkEndpoint) *ResolvedFlare {
	row := usableEndpoint(id, byID, KindFlareSolverr)
	if row == nil {
		return nil
	}
	return &ResolvedFlare{
		ID:         row.ID.String(),
		URL:        row.URL,
		Proxy:      row.FsProxy,
		Session:    row.Session,
		SessionTTL: row.SessionTTL,
		Timeout:    row.Timeout,
	}
}

// usableEndpoint looks up an endpoint by an optional id and returns it only when
// it exists, is enabled, and is of wantKind — otherwise nil (treat as absent).
func usableEndpoint(id *uuid.UUID, byID map[uuid.UUID]*ent.NetworkEndpoint, wantKind string) *ent.NetworkEndpoint {
	if id == nil {
		return nil
	}
	row, ok := byID[*id]
	if !ok || !row.Enabled || row.Kind != wantKind {
		return nil
	}
	return row
}
