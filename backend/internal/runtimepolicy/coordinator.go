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
	Bindings      map[int64]*Binding
	Endpoints     map[uuid.UUID]*Endpoint
}

type Binding struct {
	FlareMode       string
	FlareEndpointID *uuid.UUID
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
	policies, err := c.client.SourceTransportPolicy.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("runtimepolicy: query policies: %w", err)
	}
	bindings, err := c.client.SourceNetworkBinding.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("runtimepolicy: query bindings: %w", err)
	}
	endpoints, err := c.client.NetworkEndpoint.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("runtimepolicy: query endpoints: %w", err)
	}
	global := c.defaultGlobal
	row, err := c.client.Settings.Query().Where(entsettings.Key(globalSessionKey)).Only(ctx)
	if err == nil {
		global = row.Value
	} else if !ent.IsNotFound(err) {
		return fmt.Errorf("runtimepolicy: query global session: %w", err)
	}
	if p.GlobalSession != nil {
		global = *p.GlobalSession
	}

	selectedPolicies := make(map[int64]bool, len(policies))
	for _, row := range policies {
		if row.ReuseBypassSession != nil {
			selectedPolicies[row.SourceID] = *row.ReuseBypassSession
		}
	}
	for id, value := range p.Policies {
		if value == nil {
			delete(selectedPolicies, id)
		} else {
			selectedPolicies[id] = *value
		}
	}
	selectedBindings := make(map[int64]Binding, len(bindings))
	for _, row := range bindings {
		selectedBindings[row.SourceID] = Binding{FlareMode: row.FlareMode, FlareEndpointID: row.FlareEndpointID}
	}
	for id, value := range p.Bindings {
		if value == nil {
			delete(selectedBindings, id)
		} else {
			selectedBindings[id] = *value
		}
	}
	selectedEndpoints := make(map[uuid.UUID]Endpoint, len(endpoints))
	for _, row := range endpoints {
		selectedEndpoints[row.ID] = Endpoint{Kind: row.Kind, Session: row.Session, Enabled: row.Enabled}
	}
	for id, value := range p.Endpoints {
		if value == nil {
			delete(selectedEndpoints, id)
		} else {
			selectedEndpoints[id] = *value
			selectedEndpoints[id] = Endpoint{Kind: value.Kind, Session: value.Session, Enabled: value.Enabled}
		}
	}
	impacted := make(map[int64]bool)
	validateAll := p.GlobalSession != nil || (len(p.Policies) == 0 && len(p.Bindings) == 0 && len(p.Endpoints) == 0)
	for id := range p.Policies {
		impacted[id] = true
	}
	for id := range p.Bindings {
		impacted[id] = true
	}
	for sourceID, binding := range selectedBindings {
		if binding.FlareEndpointID == nil {
			continue
		}
		if _, changed := p.Endpoints[*binding.FlareEndpointID]; changed {
			impacted[sourceID] = true
		}
	}

	for sourceID, reuse := range selectedPolicies {
		if !validateAll && !impacted[sourceID] {
			continue
		}
		if !reuse {
			continue
		}
		binding, bound := selectedBindings[sourceID]
		if bound && binding.FlareMode == "none" {
			continue
		}
		session := global
		if bound && binding.FlareMode == "endpoint" && binding.FlareEndpointID != nil {
			endpoint, ok := selectedEndpoints[*binding.FlareEndpointID]
			if !ok || endpoint.Kind != "flaresolverr" || !endpoint.Enabled {
				session = global
			} else {
				session = endpoint.Session
			}
		}
		if session == "" {
			return fmt.Errorf("%w: source %d", ErrInvalidSelection, sourceID)
		}
	}
	return nil
}
