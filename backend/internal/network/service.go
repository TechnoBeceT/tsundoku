// Package network owns per-source network routing (QCAT-283): the owner defines
// named, reusable egress endpoints (a SOCKS proxy or a FlareSolverr instance)
// and binds individual sources to them, so a source can egress over a VPN /
// dedicated proxy while the rest use the global default.
//
// This package owns durable routing truth. It reads and writes NetworkEndpoint
// (the reusable endpoints), SourceNetworkBinding (the per-source assignment),
// and advances SourceRuntimeIntent in the same transaction as a changed
// binding or a referenced endpoint patch. Endpoint ids are plain UUID columns
// rather than Ent edges, so their referential rules remain enforced here. The
// package itself makes no engine calls; synchronous convergence belongs to the
// HTTP/runtime composition layer.
//
// The Ent predicate packages internal/ent/networkendpoint and
// internal/ent/sourcenetworkbinding collide with the field vocabulary and are
// imported aliased (entendpoint / entbinding) wherever needed.
package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
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

// Service owns network endpoints, source bindings, and binding-side runtime
// intent advancement. It is stateless beyond its injected stores and guards.
type Service struct {
	client            *ent.Client
	policyCoordinator *runtimepolicy.Coordinator
	runtimeIntent     *sourcetransport.Service
	sourceCatalog     sourcetransport.SourceCatalog
}

// WithRuntimePolicyCoordinator joins endpoint and routing writes to the shared
// selected-session invariant boundary.
func (s *Service) WithRuntimePolicyCoordinator(c *runtimepolicy.Coordinator) *Service {
	s.policyCoordinator = c
	return s
}

func (s *Service) mutate(ctx context.Context, proposal func(context.Context) (runtimepolicy.Proposal, error), commit func(context.Context) error) error {
	if s.policyCoordinator == nil {
		return commit(ctx)
	}
	return s.policyCoordinator.MutateDynamic(ctx, proposal, commit)
}

// sanitizeKCEFInvariantError prevents coordinator details from crossing the
// network API while preserving the caller's public validation category.
func sanitizeKCEFInvariantError(err, invalid error) error {
	if !errors.Is(err, runtimepolicy.ErrKCEFWithSocks) && !errors.Is(err, runtimepolicy.ErrInvalidKCEFPolicy) {
		return nil
	}
	return fmt.Errorf("%w: incompatible embedded browser and SOCKS route", invalid)
}

// NewService constructs a Service over the given Ent client. When supplied,
// the first catalog validates live source identity before binding persistence.
func NewService(client *ent.Client, catalogs ...sourcetransport.SourceCatalog) *Service {
	s := &Service{client: client, runtimeIntent: sourcetransport.NewService(client, nil, nil)}
	if len(catalogs) > 0 {
		s.sourceCatalog = catalogs[0]
	}
	return s
}
