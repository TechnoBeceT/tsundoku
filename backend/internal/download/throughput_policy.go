package download

import (
	"context"
	"fmt"

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
}

func (p sourceConcurrencyPolicy) For(sourceID int64) int {
	if n, ok := p.overrides[sourceID]; ok {
		return clampConcurrency(n)
	}
	return clampConcurrency(p.global)
}

type sourceConcurrencyPolicyContextKey struct{}

// BeginCycle captures the global default and every source override exactly once.
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
