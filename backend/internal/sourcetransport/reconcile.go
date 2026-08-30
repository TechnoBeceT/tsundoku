package sourcetransport

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const metadataWriteTimeout = time.Second

// RuntimeApplier converges one source's desired runtime policy. Implementations
// may rebuild a full engine snapshot because image and proxy selections are
// replace-set configuration shared by every active engine profile.
type RuntimeApplier interface {
	ApplySourceRuntime(context.Context, int64) error
}

// WithRuntimeApplier attaches the synchronous runtime convergence seam. It is
// intended for construction-time wiring before the service is shared.
func (s *Service) WithRuntimeApplier(applier RuntimeApplier) *Service {
	s.applier = applier
	return s
}

// ApplyPending applies the latest still-pending revision for sourceID. Calls are
// serialized across the service and re-read after acquiring the latch, so
// concurrent retries coalesce once an earlier caller has already acknowledged
// the same revision. A revision created while the external apply is running
// cannot be falsely acknowledged because MarkApplied is guarded by the exact
// attempted desired revision.
func (s *Service) ApplyPending(ctx context.Context, sourceID int64) (Intent, error) {
	if err := s.applySem.Acquire(ctx, 1); err != nil {
		return Intent{}, fmt.Errorf("sourcetransport.ApplyPending source %d acquire runtime apply: %w", sourceID, err)
	}
	defer s.applySem.Release(1)

	intent, err := s.loadIntent(ctx, sourceID)
	if err != nil {
		return Intent{}, fmt.Errorf("sourcetransport.ApplyPending source %d: %w", sourceID, err)
	}
	if intent.DesiredRevision <= intent.AppliedRevision {
		return intent, nil
	}
	if s.applier == nil {
		return intent, fmt.Errorf("sourcetransport.ApplyPending source %d: runtime applier is not configured", sourceID)
	}

	attemptedRevision := intent.DesiredRevision
	if applyErr := s.applier.ApplySourceRuntime(ctx, sourceID); applyErr != nil {
		metadataCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metadataWriteTimeout)
		markErr := s.MarkPending(metadataCtx, sourceID, attemptedRevision, applyErr.Error())
		cancel()
		current, loadErr := s.loadIntent(ctx, sourceID)
		return current, errors.Join(applyErr, markErr, loadErr)
	}
	if err := s.MarkApplied(ctx, sourceID, attemptedRevision); err != nil {
		return Intent{}, err
	}
	current, err := s.loadIntent(ctx, sourceID)
	if err != nil {
		return Intent{}, fmt.Errorf("sourcetransport.ApplyPending source %d: reload intent: %w", sourceID, err)
	}
	return current, nil
}

// ReconcilePending performs one bounded, source-ordered retry pass over the
// durable pending set. Each source is attempted at most once in this scan;
// failures are joined after the remaining sources have had their chance.
func (s *Service) ReconcilePending(ctx context.Context) error {
	pending, err := s.Pending(ctx)
	if err != nil {
		return fmt.Errorf("sourcetransport.ReconcilePending: %w", err)
	}
	var errs []error
	for _, intent := range pending {
		if _, err := s.ApplyPending(ctx, intent.SourceID); err != nil {
			errs = append(errs, fmt.Errorf("source %d: %w", intent.SourceID, err))
		}
	}
	return errors.Join(errs...)
}
