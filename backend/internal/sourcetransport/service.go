package sourcetransport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entintent "github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
)

// Service persists and resolves per-source transport policy and runtime intent.
type Service struct {
	client   *ent.Client
	defaults Defaults
	catalog  SourceCatalog
	applier  RuntimeApplier
	applyMu  sync.Mutex
}

// NewService constructs a source transport service.
func NewService(client *ent.Client, defaults Defaults, catalog SourceCatalog) *Service {
	return &Service{client: client, defaults: defaults, catalog: catalog}
}

// Resolve returns one source's stored overrides resolved through the current
// runtime defaults.
func (s *Service) Resolve(ctx context.Context, sourceID int64) (Effective, error) {
	override, err := s.loadOverride(ctx, sourceID)
	if err != nil {
		return Effective{}, fmt.Errorf("sourcetransport.Resolve: %w", err)
	}
	return s.resolveOverride(ctx, sourceID, override)
}

// Snapshot loads all persisted source overrides in one query.
func (s *Service) Snapshot(ctx context.Context) (map[int64]Override, error) {
	rows, err := s.client.SourceTransportPolicy.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sourcetransport.Snapshot: query policies: %w", err)
	}
	snapshot := make(map[int64]Override, len(rows))
	for _, row := range rows {
		snapshot[row.SourceID] = overrideFromRow(row)
	}
	return snapshot, nil
}

// Update validates the live source before beginning its transaction, then
// mutates policy and advances desired runtime intent atomically.
func (s *Service) Update(ctx context.Context, sourceID int64, patch Patch) (UpdateResult, error) {
	if err := validatePatch(patch); err != nil {
		return UpdateResult{}, err
	}
	if err := s.catalog.RequireSource(ctx, sourceID); err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: require source %d: %w", sourceID, err)
	}

	stored, err := s.loadOverride(ctx, sourceID)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: %w", err)
	}
	// Preflight before beginning the write transaction. In particular, an
	// explicit reusable session with no selected session fails here without
	// creating a policy or intent row.
	if _, err := s.resolveOverride(ctx, sourceID, applyPatch(stored, patch)); err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: resolve source %d: %w", sourceID, err)
	}
	if patch.ReuseBypassSession.Operation == PatchKeep && patch.ImageConnectionMode.Operation == PatchKeep {
		intent, err := s.loadIntent(ctx, sourceID)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("sourcetransport.Update: %w", err)
		}
		effective, err := s.resolveOverride(ctx, sourceID, stored)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("sourcetransport.Update: resolve source %d: %w", sourceID, err)
		}
		return UpdateResult{Override: stored, Effective: effective, Intent: intent}, nil
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: begin transaction: %w", err)
	}
	if err := s.persistPatchTx(ctx, tx, sourceID, patch); err != nil {
		_ = tx.Rollback()
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: %w", err)
	}
	intent, err := s.AdvanceIntentTx(ctx, tx, sourceID)
	if err != nil {
		_ = tx.Rollback()
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: advance source %d intent: %w", sourceID, err)
	}
	override, err := loadOverrideTx(ctx, tx, sourceID)
	if err != nil {
		_ = tx.Rollback()
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: commit source %d: %w", sourceID, err)
	}
	effective, err := s.resolveOverride(ctx, sourceID, override)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: resolve source %d: %w", sourceID, err)
	}
	result := UpdateResult{Override: override, Effective: effective, Intent: intent}
	if s.applier == nil {
		return result, nil
	}
	appliedIntent, err := s.ApplyPending(ctx, sourceID)
	if appliedIntent.DesiredRevision == result.Intent.DesiredRevision {
		result.Intent = appliedIntent
	}
	if err != nil {
		return result, fmt.Errorf("sourcetransport.Update: apply source %d runtime: %w", sourceID, err)
	}
	return result, nil
}

// AdvanceIntentTx atomically advances desired runtime intent inside a caller's
// transaction. Proxy and network owners use this primitive alongside their own
// policy mutations so handlers never advance an intent separately.
func (s *Service) AdvanceIntentTx(ctx context.Context, tx *ent.Tx, sourceID int64) (Intent, error) {
	now := time.Now()
	if err := tx.SourceRuntimeIntent.Create().SetSourceID(sourceID).SetDesiredRevision(1).
		OnConflictColumns(entintent.FieldSourceID).
		Update(func(update *ent.SourceRuntimeIntentUpsert) {
			update.AddDesiredRevision(1)
			update.SetUpdatedAt(now)
		}).
		Exec(ctx); err != nil {
		return Intent{}, fmt.Errorf("upsert source %d runtime intent: %w", sourceID, err)
	}
	row, err := tx.SourceRuntimeIntent.Query().Where(entintent.SourceID(sourceID)).Only(ctx)
	if err != nil {
		return Intent{}, fmt.Errorf("query source %d advanced runtime intent: %w", sourceID, err)
	}
	return intentFromRow(row), nil
}

// MarkApplied records a successful apply only when revision remains current.
// A stale acknowledgement cannot acknowledge a newer desired revision.
func (s *Service) MarkApplied(ctx context.Context, sourceID, revision int64) error {
	_, err := s.client.SourceRuntimeIntent.Update().Where(
		entintent.SourceID(sourceID),
		entintent.DesiredRevisionEQ(revision),
	).SetAppliedRevision(revision).SetLastApplyAttempt(time.Now()).SetLastApplyError("").Save(ctx)
	if err != nil {
		return fmt.Errorf("sourcetransport.MarkApplied source %d revision %d: %w", sourceID, revision, err)
	}
	return nil
}

// MarkPending records a failed apply only while revision remains current.
func (s *Service) MarkPending(ctx context.Context, sourceID, revision int64, applyError string) error {
	_, err := s.client.SourceRuntimeIntent.Update().Where(
		entintent.SourceID(sourceID),
		entintent.DesiredRevisionEQ(revision),
	).SetLastApplyAttempt(time.Now()).SetLastApplyError(sanitizeApplyError(applyError)).Save(ctx)
	if err != nil {
		return fmt.Errorf("sourcetransport.MarkPending source %d revision %d: %w", sourceID, revision, err)
	}
	return nil
}

// Pending returns intents whose desired revision still exceeds the applied one.
func (s *Service) Pending(ctx context.Context) ([]Intent, error) {
	rows, err := s.client.SourceRuntimeIntent.Query().Where(
		entintent.DesiredRevisionGT(0),
	).Order(entintent.BySourceID()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sourcetransport.Pending: query intents: %w", err)
	}
	intents := make([]Intent, 0, len(rows))
	for _, row := range rows {
		if row.DesiredRevision > row.AppliedRevision {
			intents = append(intents, intentFromRow(row))
		}
	}
	return intents, nil
}

func (s *Service) resolveOverride(ctx context.Context, sourceID int64, override Override) (Effective, error) {
	reuse, mode, err := s.defaults.ResolveBypassSession(ctx, sourceID, override.ReuseBypassSession)
	if err != nil {
		return Effective{}, err
	}
	imageMode := s.defaults.ImageConnectionMode(ctx)
	if override.ImageConnectionMode != nil {
		imageMode = *override.ImageConnectionMode
	}
	return Effective{ReuseBypassSession: reuse, BypassSessionMode: mode, ImageConnectionMode: imageMode}, nil
}
