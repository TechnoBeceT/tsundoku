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
	return s.applyWithLifecycle(ctx, sourceID, nil)
}

// ApplyRevision applies sourceID only while revision is still the exact desired
// revision. A newer commit supersedes the request and remains pending; this
// method never applies or acknowledges that different revision on the older
// commit's behalf.
func (s *Service) ApplyRevision(ctx context.Context, sourceID, revision int64) (Intent, error) {
	return s.applyWithLifecycle(ctx, sourceID, &revision)
}

// ApplyRevisions converges a set of exact committed source revisions with at
// most one full runtime apply. Requested sources and returned current intents
// are source-ordered. Revisions that are already applied or no longer current
// are excluded before convergence; exact-revision guards on the final bulk
// metadata write keep a revision changed during convergence pending.
func (s *Service) ApplyRevisions(ctx context.Context, revisions []Intent) ([]Intent, error) {
	sourceIDs, requested := normalizeRevisionRequests(revisions)
	if len(sourceIDs) == 0 {
		return []Intent{}, nil
	}
	if lifecycle, ok := s.applier.(interface {
		RunRuntime(context.Context, func(context.Context) error) error
	}); ok {
		var intents []Intent
		err := lifecycle.RunRuntime(ctx, func(ctx context.Context) error {
			var applyErr error
			intents, applyErr = s.applyRevisions(ctx, sourceIDs, requested)
			return applyErr
		})
		return intents, err
	}
	return s.applyRevisions(ctx, sourceIDs, requested)
}

func (s *Service) applyWithLifecycle(ctx context.Context, sourceID int64, requiredRevision *int64) (Intent, error) {
	if lifecycle, ok := s.applier.(interface {
		RunRuntime(context.Context, func(context.Context) error) error
	}); ok {
		var intent Intent
		err := lifecycle.RunRuntime(ctx, func(ctx context.Context) error {
			var applyErr error
			intent, applyErr = s.applyPending(ctx, sourceID, requiredRevision)
			return applyErr
		})
		return intent, err
	}
	return s.applyPending(ctx, sourceID, requiredRevision)
}

func (s *Service) applyPending(ctx context.Context, sourceID int64, requiredRevision *int64) (Intent, error) {
	if err := s.applySem.Acquire(ctx, 1); err != nil {
		return Intent{}, fmt.Errorf("sourcetransport.ApplyPending source %d acquire runtime apply: %w", sourceID, err)
	}
	defer s.applySem.Release(1)

	intent, err := s.loadIntent(ctx, sourceID)
	if err != nil {
		return Intent{}, fmt.Errorf("sourcetransport.ApplyPending source %d: %w", sourceID, err)
	}
	if requiredRevision != nil && intent.DesiredRevision != *requiredRevision {
		return intent, nil
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

func (s *Service) applyRevisions(ctx context.Context, sourceIDs []int64, requested map[int64]map[int64]struct{}) ([]Intent, error) {
	if err := s.applySem.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("sourcetransport.ApplyRevisions sources %v acquire runtime apply: %w", sourceIDs, err)
	}
	defer s.applySem.Release(1)

	current, err := s.loadIntents(ctx, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("sourcetransport.ApplyRevisions sources %v: %w", sourceIDs, err)
	}
	pending := exactPendingRevisions(current, requested)
	if len(pending) == 0 {
		return current, nil
	}
	if s.applier == nil {
		return current, fmt.Errorf("sourcetransport.ApplyRevisions sources %v: runtime applier is not configured", sourceIDs)
	}

	if applyErr := s.applier.ApplySourceRuntime(ctx, pending[0].SourceID); applyErr != nil {
		metadataCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metadataWriteTimeout)
		markErr := s.markRevisionsPending(metadataCtx, pending, applyErr.Error())
		cancel()
		latest, loadErr := s.loadIntents(ctx, sourceIDs)
		return latest, errors.Join(applyErr, markErr, loadErr)
	}
	if err := s.markRevisionsApplied(ctx, pending); err != nil {
		return nil, err
	}
	latest, err := s.loadIntents(ctx, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("sourcetransport.ApplyRevisions sources %v: reload intents: %w", sourceIDs, err)
	}
	return latest, nil
}

func normalizeRevisionRequests(revisions []Intent) ([]int64, map[int64]map[int64]struct{}) {
	requested := make(map[int64]map[int64]struct{}, len(revisions))
	sourceIDs := make([]int64, 0, len(revisions))
	for _, intent := range revisions {
		byRevision, ok := requested[intent.SourceID]
		if !ok {
			byRevision = make(map[int64]struct{})
			requested[intent.SourceID] = byRevision
			sourceIDs = append(sourceIDs, intent.SourceID)
		}
		byRevision[intent.DesiredRevision] = struct{}{}
	}
	sourceIDs = sortedUniqueSourceIDs(sourceIDs)
	return sourceIDs, requested
}

func exactPendingRevisions(current []Intent, requested map[int64]map[int64]struct{}) []Intent {
	pending := make([]Intent, 0, len(current))
	for _, intent := range current {
		if intent.DesiredRevision <= intent.AppliedRevision {
			continue
		}
		if _, exact := requested[intent.SourceID][intent.DesiredRevision]; exact {
			pending = append(pending, intent)
		}
	}
	return pending
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
