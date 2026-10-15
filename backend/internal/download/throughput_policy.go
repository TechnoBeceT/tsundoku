package download

import (
	"context"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
)

// SourceThroughputPolicies supplies one bulk snapshot of persisted source
// overrides. *sourcethroughput.Service satisfies this port.
type SourceThroughputPolicies interface {
	Snapshot(context.Context) (map[int64]sourcethroughput.Override, error)
}

// sourceConcurrencyPolicy is immutable after construction. A source absent from
// overrides inherits the cycle's captured global download concurrency.
type sourceConcurrencyPolicy struct {
	global    int
	overrides map[int64]int
	byLabel   map[string]int
}

func (p sourceConcurrencyPolicy) For(sourceID int64) int {
	if n, ok := p.overrides[sourceID]; ok {
		return clampConcurrency(n)
	}
	return clampConcurrency(p.global)
}

// ForSource resolves the shared physical-source bound before any alias starts.
// Distinct live IDs with one accepted canonical label share the lowest cap.
func (p sourceConcurrencyPolicy) ForSource(key string, sourceID int64) int {
	if n, ok := p.byLabel[key]; ok {
		return n
	}
	return p.For(sourceID)
}

func (d *Dispatcher) resolveSourceLabels(ctx context.Context, policy *sourceConcurrencyPolicy) error {
	if d.client == nil {
		return nil
	}
	var providers []struct {
		Provider     string `json:"provider"`
		ProviderName string `json:"provider_name"`
	}
	err := d.client.SeriesProvider.Query().GroupBy(entprovider.FieldProvider, entprovider.FieldProviderName).Scan(ctx, &providers)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.BeginCycle: source identities: %w", err)
	}
	policy.byLabel = make(map[string]int)
	for _, row := range providers {
		sp := &ent.SeriesProvider{Provider: row.Provider, ProviderName: row.ProviderName}
		id := linkedSourceID(sp)
		if id == 0 {
			continue
		}
		key, cap := canonicalSourceKey(sp), policy.For(id)
		if old, ok := policy.byLabel[key]; !ok || cap < old {
			policy.byLabel[key] = cap
		}
	}
	return nil
}

type sourceConcurrencyPolicyContextKey struct{}

// BeginCycle captures the default, source overrides and canonical live aliases
// exactly once, so an unknown disk alias cannot start under a looser default
// while its recognized live provider lies beyond the current discovery page.
// The returned context must be passed through every download and upgrade pass in
// that cycle. A load error is returned before any chapter selection occurs.
func (d *Dispatcher) BeginCycle(ctx context.Context) (context.Context, error) {
	policy := sourceConcurrencyPolicy{
		global:    d.downloadConcurrency(ctx),
		overrides: map[int64]int{},
	}
	if d.throughput != nil {
		stored, err := d.throughput.Snapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("download.Dispatcher.BeginCycle: snapshot source throughput: %w", err)
		}
		for sourceID, override := range stored {
			if override.DownloadConcurrency != nil {
				policy.overrides[sourceID] = clampConcurrency(*override.DownloadConcurrency)
			}
		}
	}
	if err := d.resolveSourceLabels(ctx, &policy); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, sourceConcurrencyPolicyContextKey{}, policy), nil
}

func (d *Dispatcher) concurrencyPolicy(ctx context.Context) (sourceConcurrencyPolicy, error) {
	if policy, ok := ctx.Value(sourceConcurrencyPolicyContextKey{}).(sourceConcurrencyPolicy); ok {
		return policy, nil
	}
	cycleCtx, err := d.BeginCycle(ctx)
	if err != nil {
		return sourceConcurrencyPolicy{}, err
	}
	return cycleCtx.Value(sourceConcurrencyPolicyContextKey{}).(sourceConcurrencyPolicy), nil
}

func clampConcurrency(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
