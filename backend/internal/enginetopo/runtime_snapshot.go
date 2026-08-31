package enginetopo

import (
	"context"
	"fmt"
	"sort"

	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

// SourceTransportSnapshotter is the narrow source-policy read surface needed
// to compose engine-route profile inputs. *sourcetransport.Service satisfies it.
type SourceTransportSnapshotter interface {
	Snapshot(context.Context) (map[int64]sourcetransport.Override, error)
}

// runtimeBinding is the topology-owned projection of an explicit network
// binding plus source transport policy. Keeping the disposable-session bit here
// prevents the DB-truth network package from learning source-policy semantics.
type runtimeBinding struct {
	network.ResolvedBinding
	DisableBypassSession bool
}

// composeRuntimeSnapshot returns the deterministic union of explicit network
// bindings and policy-only sources whose stored session override is Off. An Off
// source without a SourceNetworkBinding receives an otherwise-global binding so
// profile derivation can isolate it on a blank-session instance. Inherit and On
// do not add a binding by themselves because they retain the default profile.
func composeRuntimeSnapshot(
	ctx context.Context,
	networkSnapshot NetworkSnapshotter,
	transportSnapshot SourceTransportSnapshotter,
) ([]runtimeBinding, error) {
	bindings, err := networkSnapshot.RoutingSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("network bindings: %w", err)
	}

	bySource := make(map[int64]runtimeBinding, len(bindings))
	for _, binding := range bindings {
		bySource[binding.SourceID] = runtimeBinding{ResolvedBinding: binding}
	}
	if err := addDisposableSessionBindings(ctx, transportSnapshot, bySource); err != nil {
		return nil, err
	}

	sourceIDs := make([]int64, 0, len(bySource))
	for sourceID := range bySource {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Slice(sourceIDs, func(i, j int) bool { return sourceIDs[i] < sourceIDs[j] })
	out := make([]runtimeBinding, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		out = append(out, bySource[sourceID])
	}
	return out, nil
}

func addDisposableSessionBindings(ctx context.Context, snapshot SourceTransportSnapshotter, bySource map[int64]runtimeBinding) error {
	if snapshot == nil {
		return nil
	}
	policies, err := snapshot.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("source transport policies: %w", err)
	}
	for sourceID, policy := range policies {
		if policy.ReuseBypassSession == nil || *policy.ReuseBypassSession {
			continue
		}
		binding, ok := bySource[sourceID]
		if !ok {
			binding.ResolvedBinding = network.ResolvedBinding{SourceID: sourceID, FlareMode: network.FlareModeGlobal}
		}
		binding.DisableBypassSession = true
		bySource[sourceID] = binding
	}
	return nil
}

// SessionPolicyResolver resolves one source's selected global/endpoint session
// for sourcetransport mutation preflight. It composes network routing with the
// global engine config here, outside the network domain that owns neither
// source-policy semantics nor the global FlareSolverr settings.
type SessionPolicyResolver struct {
	network NetworkSnapshotter
	base    ConfigProvider
}

// NewSessionPolicyResolver constructs a source session-policy resolver.
func NewSessionPolicyResolver(networkSnapshot NetworkSnapshotter, base ConfigProvider) SessionPolicyResolver {
	return SessionPolicyResolver{network: networkSnapshot, base: base}
}

// ResolveBypassSession returns whether the selected session is reusable and its
// effective mode. Flare mode none is always disabled. For global/endpoint mode,
// Off is disposable, Inherit follows the configured session exactly, and On
// requires a nonblank configured session.
func (r SessionPolicyResolver) ResolveBypassSession(
	ctx context.Context,
	sourceID int64,
	override *bool,
) (bool, sourcetransport.BypassSessionMode, error) {
	bindings, err := r.network.RoutingSnapshot(ctx)
	if err != nil {
		return false, sourcetransport.BypassSessionDisabled,
			fmt.Errorf("enginetopo.ResolveBypassSession: snapshot: %w", err)
	}

	mode, session := selectedBypassSession(bindings, sourceID, r.base.FlareSolverrSessionName(ctx))
	if mode == network.FlareModeNone {
		return false, sourcetransport.BypassSessionDisabled, nil
	}
	if override != nil && !*override {
		return false, sourcetransport.BypassSessionDisposable, nil
	}
	if session == "" {
		if override != nil && *override {
			return false, sourcetransport.BypassSessionDisabled,
				fmt.Errorf("reusable bypass session requires a nonblank selected session")
		}
		return false, sourcetransport.BypassSessionDisposable, nil
	}
	return true, sourcetransport.BypassSessionReusable, nil
}

func selectedBypassSession(bindings []network.ResolvedBinding, sourceID int64, global string) (string, string) {
	for _, binding := range bindings {
		if binding.SourceID != sourceID {
			continue
		}
		if binding.FlareMode == network.FlareModeEndpoint && binding.Flare != nil {
			return binding.FlareMode, binding.Flare.Session
		}
		return binding.FlareMode, global
	}
	return network.FlareModeGlobal, global
}
