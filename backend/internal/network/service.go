// Package network owns per-source network routing (QCAT-283): the owner defines
// named, reusable egress endpoints (a SOCKS proxy or a FlareSolverr instance)
// and binds individual sources to them, so a source can egress over a VPN /
// dedicated proxy while the rest use the global default.
//
// This package is DB-TRUTH ONLY. It reads and writes two Ent entities —
// NetworkEndpoint (the reusable endpoints) and SourceNetworkBinding (the
// per-source assignment) — and enforces referential integrity in code (the
// endpoint ids are plain UUID columns, not Ent edges). The engine push that
// makes a binding actually route traffic is a SEPARATE later slice; nothing in
// this package calls the engine or internal/enginetopo.
//
// The Ent predicate packages internal/ent/networkendpoint and
// internal/ent/sourcenetworkbinding collide with the field vocabulary and are
// imported aliased (entendpoint / entbinding) wherever needed.
package network

import (
	"errors"

	"github.com/technobecet/tsundoku/internal/ent"
)

// KindSocks / KindFlareSolverr are the two NetworkEndpoint kinds.
const (
	KindSocks        = "socks"
	KindFlareSolverr = "flaresolverr"
)

// FlareMode values — the per-source FlareSolverr routing scope on a binding.
const (
	FlareModeNone     = "none"
	FlareModeGlobal   = "global"
	FlareModeEndpoint = "endpoint"
)

// ErrEndpointNotFound is returned when no endpoint matches the given id. The
// HTTP handler maps it to a 404.
var ErrEndpointNotFound = errors.New("network endpoint not found")

// ErrEndpointInUse is returned by DeleteEndpoint when at least one binding still
// references the endpoint (owner-safety bias — an in-use endpoint can never be
// deleted out from under a source). The wrapped message lists the referencing
// source ids. The HTTP handler maps it to a 409.
var ErrEndpointInUse = errors.New("network endpoint is in use")

// ErrInvalidEndpoint is returned when an endpoint's fields fail validation
// (unknown kind, blank name, or a bad SOCKS/FlareSolverr field). The wrapped
// message names the offending field. The HTTP handler maps it to a 400.
var ErrInvalidEndpoint = errors.New("invalid network endpoint")

// ErrInvalidBinding is returned when a binding fails validation (a referenced
// endpoint is missing or the wrong kind, an unknown flare_mode, or
// flare_endpoint_id present/absent inconsistent with flare_mode). The HTTP
// handler maps it to a 400.
var ErrInvalidBinding = errors.New("invalid network binding")

// ErrBindingNotFound is returned by GetBinding/ClearBinding when no binding
// exists for the given source id. The HTTP handler maps it to a 404.
var ErrBindingNotFound = errors.New("network binding not found")

// Service is the per-source network-routing domain service over the
// NetworkEndpoint + SourceNetworkBinding tables. It is stateless beyond the Ent
// client and makes no engine calls.
type Service struct {
	client *ent.Client
}

// NewService constructs a Service over the given Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}
