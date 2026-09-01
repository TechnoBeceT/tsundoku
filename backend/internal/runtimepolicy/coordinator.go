// Package runtimepolicy serializes and validates authoritative mutations whose
// combination selects the reusable FlareSolverr session for a source.
package runtimepolicy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/ent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
	"golang.org/x/sync/semaphore"
)

const globalSessionKey = "flaresolverr.session_name"

// ErrInvalidSelection reports an explicit reusable-session policy whose
// selected global or endpoint session is blank.
var ErrInvalidSelection = errors.New("reusable bypass session requires a nonblank selected session")

// Proposal describes the prospective portion of one serialized mutation.
type Proposal struct {
	GlobalSession *string
	Policies      map[int64]*bool
	KCEFPolicies  map[int64]*KCEFPolicy
	Bindings      map[int64]*Binding
	Endpoints     map[uuid.UUID]*Endpoint
}

type Binding struct {
	FlareMode       string
	FlareEndpointID *uuid.UUID
	HasSocks        bool
}

type Endpoint struct {
	Kind, Session string
	Enabled       bool
}

// Coordinator is a cancelable, process-wide mutation gate. Every service that
// can change the selected session shares one instance.
type Coordinator struct {
	client        *ent.Client
	defaultGlobal string
	gate          *semaphore.Weighted
}

type admissionKey struct{}

func New(client *ent.Client, defaultGlobal string) *Coordinator {
	return &Coordinator{client: client, defaultGlobal: defaultGlobal, gate: semaphore.NewWeighted(1)}
}

// Mutate validates the prospective state and runs commit without allowing a
// competing authoritative mutation between those two operations.
func (c *Coordinator) Mutate(ctx context.Context, proposal Proposal, commit func(context.Context) error) error {
	return c.MutateDynamic(ctx, func(context.Context) (Proposal, error) { return proposal, nil }, commit)
}

// MutateDynamic derives a proposal while holding the gate, for patches whose
// prospective state depends on a current row.
func (c *Coordinator) MutateDynamic(ctx context.Context, proposal func(context.Context) (Proposal, error), commit func(context.Context) error) error {
	if err := c.gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.gate.Release(1)
	p, err := proposal(ctx)
	if err != nil {
		return err
	}
	if err := c.validate(ctx, p); err != nil {
		return err
	}
	return commit(context.WithValue(ctx, admissionKey{}, c))
}

// ValidateCurrent fails closed when durable state contains an impossible
// explicit reusable-session selection (including legacy/direct DB writes).
func (c *Coordinator) ValidateCurrent(ctx context.Context) error {
	if admitted, _ := ctx.Value(admissionKey{}).(*Coordinator); admitted == c {
		return c.validate(ctx, Proposal{})
	}
	if err := c.gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.gate.Release(1)
	return c.validate(ctx, Proposal{})
}

func (c *Coordinator) validate(ctx context.Context, p Proposal) error {
	state, err := c.prospectiveState(ctx, p)
	if err != nil {
		return err
	}
	for sourceID, reuse := range state.policies {
		if !state.shouldValidate(sourceID) || !reuse {
			continue
		}
		if state.selectedSession(sourceID) == "" {
			return fmt.Errorf("%w: source %d", ErrInvalidSelection, sourceID)
		}
	}
	for sourceID, policy := range state.kcefPolicies {
		if !state.shouldValidate(sourceID) {
			continue
		}
		binding := state.bindings[sourceID]
		if _, err := ResolveKCEF(policy, binding.HasSocks, binding.FlareMode); err != nil {
			return fmt.Errorf("source %d: %w", sourceID, err)
		}
	}
	return nil
}

type prospectiveState struct {
	global       string
	policies     map[int64]bool
	kcefPolicies map[int64]KCEFPolicy
	bindings     map[int64]Binding
	endpoints    map[uuid.UUID]Endpoint
	impacted     map[int64]bool
	validateAll  bool
}

func (c *Coordinator) prospectiveState(ctx context.Context, p Proposal) (prospectiveState, error) {
	policies, err := c.client.SourceTransportPolicy.Query().All(ctx)
	if err != nil {
		return prospectiveState{}, fmt.Errorf("runtimepolicy: query policies: %w", err)
	}
	bindings, err := c.client.SourceNetworkBinding.Query().All(ctx)
	if err != nil {
		return prospectiveState{}, fmt.Errorf("runtimepolicy: query bindings: %w", err)
	}
	endpoints, err := c.client.NetworkEndpoint.Query().All(ctx)
	if err != nil {
		return prospectiveState{}, fmt.Errorf("runtimepolicy: query endpoints: %w", err)
	}
	global, err := c.prospectiveGlobalSession(ctx, p.GlobalSession)
	if err != nil {
		return prospectiveState{}, err
	}
	state := newProspectiveState(global, policies, bindings, endpoints, p)
	applyOptionalMap(state.policies, p.Policies)
	applyOptionalMap(state.kcefPolicies, p.KCEFPolicies)
	applyOptionalMap(state.bindings, p.Bindings)
	applyOptionalMap(state.endpoints, p.Endpoints)
	for id := range p.Policies {
		state.impacted[id] = true
	}
	for id := range p.KCEFPolicies {
		state.impacted[id] = true
	}
	for id := range p.Bindings {
		state.impacted[id] = true
	}
	for sourceID, binding := range state.bindings {
		if binding.FlareEndpointID != nil {
			if _, changed := p.Endpoints[*binding.FlareEndpointID]; changed {
				state.impacted[sourceID] = true
			}
		}
	}
	return state, nil
}

func newProspectiveState(global string, policies []*ent.SourceTransportPolicy, bindings []*ent.SourceNetworkBinding, endpoints []*ent.NetworkEndpoint, p Proposal) prospectiveState {
	state := prospectiveState{
		global: global, policies: make(map[int64]bool, len(policies)),
		bindings: make(map[int64]Binding, len(bindings)), endpoints: make(map[uuid.UUID]Endpoint, len(endpoints)),
		kcefPolicies: make(map[int64]KCEFPolicy), impacted: make(map[int64]bool), validateAll: p.GlobalSession != nil || (len(p.Policies) == 0 && len(p.KCEFPolicies) == 0 && len(p.Bindings) == 0 && len(p.Endpoints) == 0),
	}
	for _, row := range policies {
		if row.ReuseBypassSession != nil {
			state.policies[row.SourceID] = *row.ReuseBypassSession
		}
		if row.KcefPolicy != nil {
			state.kcefPolicies[row.SourceID] = KCEFPolicy(*row.KcefPolicy)
		}
	}
	for _, row := range endpoints {
		state.endpoints[row.ID] = Endpoint{Kind: row.Kind, Session: row.Session, Enabled: row.Enabled}
	}
	for _, row := range bindings {
		state.bindings[row.SourceID] = Binding{
			FlareMode: row.FlareMode, FlareEndpointID: row.FlareEndpointID,
			HasSocks: effectiveSocksEndpoint(row.SocksEndpointID, state.endpoints),
		}
	}
	return state
}

func effectiveSocksEndpoint(id *uuid.UUID, endpoints map[uuid.UUID]Endpoint) bool {
	if id == nil {
		return false
	}
	endpoint, ok := endpoints[*id]
	return ok && endpoint.Enabled && endpoint.Kind == "socks"
}

func (c *Coordinator) prospectiveGlobalSession(ctx context.Context, proposed *string) (string, error) {
	global := c.defaultGlobal
	row, err := c.client.Settings.Query().Where(entsettings.Key(globalSessionKey)).Only(ctx)
	if err == nil {
		global = row.Value
	} else if !ent.IsNotFound(err) {
		return "", fmt.Errorf("runtimepolicy: query global session: %w", err)
	}
	if proposed != nil {
		global = *proposed
	}
	return global, nil
}

func applyOptionalMap[K comparable, V any](target map[K]V, patch map[K]*V) {
	for id, value := range patch {
		if value == nil {
			delete(target, id)
			continue
		}
		target[id] = *value
	}
}

func (s prospectiveState) shouldValidate(sourceID int64) bool {
	return s.validateAll || s.impacted[sourceID]
}

func (s prospectiveState) selectedSession(sourceID int64) string {
	binding, bound := s.bindings[sourceID]
	if bound && binding.FlareMode == "none" {
		return "disabled"
	}
	if !bound || binding.FlareMode != "endpoint" || binding.FlareEndpointID == nil {
		return s.global
	}
	endpoint, ok := s.endpoints[*binding.FlareEndpointID]
	if !ok || endpoint.Kind != "flaresolverr" || !endpoint.Enabled {
		return s.global
	}
	return endpoint.Session
}
