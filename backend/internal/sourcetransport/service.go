package sourcetransport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entintent "github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"golang.org/x/sync/semaphore"
)

// Service persists and resolves per-source transport policy and runtime intent.
type Service struct {
	client            *ent.Client
	defaults          Defaults
	catalog           SourceCatalog
	applier           RuntimeApplier
	applySem          *semaphore.Weighted
	policyCoordinator *runtimepolicy.Coordinator
}

// WithRuntimePolicyCoordinator joins source-policy mutations to the shared
// selected-session invariant boundary.
func (s *Service) WithRuntimePolicyCoordinator(c *runtimepolicy.Coordinator) *Service {
	s.policyCoordinator = c
	return s
}

// NewService constructs a source transport service.
func NewService(client *ent.Client, defaults Defaults, catalog SourceCatalog) *Service {
	return &Service{
		client:   client,
		defaults: defaults,
		catalog:  catalog,
		applySem: semaphore.NewWeighted(1),
	}
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
	if s.policyCoordinator != nil {
		if err := s.policyCoordinator.ValidateCurrent(ctx); err != nil {
			return nil, fmt.Errorf("sourcetransport.Snapshot: %w", err)
		}
	}
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
	if s.policyCoordinator == nil {
		return s.update(ctx, sourceID, patch, true, false)
	}
	if err := validatePatch(patch); err != nil {
		return UpdateResult{}, err
	}
	if err := s.catalog.RequireSource(ctx, sourceID); err != nil {
		return UpdateResult{}, fmt.Errorf("sourcetransport.Update: require source %d: %w", sourceID, err)
	}
	var result UpdateResult
	var updateErr error
	err := s.policyCoordinator.MutateDynamic(ctx, func(ctx context.Context) (runtimepolicy.Proposal, error) {
		stored, err := s.loadOverride(ctx, sourceID)
		if err != nil {
			return runtimepolicy.Proposal{}, err
		}
		return runtimepolicy.Proposal{Policies: map[int64]*bool{sourceID: applyPatch(stored, patch).ReuseBypassSession}}, nil
	}, func(ctx context.Context) error {
		result, updateErr = s.update(ctx, sourceID, patch, false, true)
		return updateErr
	})
	if err != nil {
		if errors.Is(err, runtimepolicy.ErrInvalidSelection) {
			return result, fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
		}
		return result, fmt.Errorf("sourcetransport.Update: %w", err)
	}
	if patch.ReuseBypassSession.Operation == PatchKeep && patch.ImageConnectionMode.Operation == PatchKeep {
		return result, nil
	}
	return s.applyUpdate(ctx, sourceID, result)
}

func (s *Service) update(ctx context.Context, sourceID int64, patch Patch, apply, prevalidated bool) (UpdateResult, error) {
	if !prevalidated {
		if err := validatePatch(patch); err != nil {
			return UpdateResult{}, err
		}
		if err := s.catalog.RequireSource(ctx, sourceID); err != nil {
			return UpdateResult{}, fmt.Errorf("sourcetransport.Update: require source %d: %w", sourceID, err)
		}
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
	if !apply {
		return result, nil
	}
	return s.applyUpdate(ctx, sourceID, result)
}

func (s *Service) applyUpdate(ctx context.Context, sourceID int64, result UpdateResult) (UpdateResult, error) {
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
	intents, err := s.AdvanceIntentsTx(ctx, tx, []int64{sourceID})
	if err != nil {
		return Intent{}, err
	}
	return intents[0], nil
}

// AdvanceIntentsTx advances every source's desired runtime revision with one
// bulk upsert and reloads the exact committed revisions with one bounded query.
// Sorting the unique ids also gives concurrent multi-source writers one stable
// row-lock order.
func (s *Service) AdvanceIntentsTx(ctx context.Context, tx *ent.Tx, sourceIDs []int64) ([]Intent, error) {
	sourceIDs = sortedUniqueSourceIDs(sourceIDs)
	if len(sourceIDs) == 0 {
		return []Intent{}, nil
	}
	now := time.Now()
	builders := make([]*ent.SourceRuntimeIntentCreate, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		builders = append(builders, tx.SourceRuntimeIntent.Create().SetSourceID(sourceID).SetDesiredRevision(1))
	}
	if err := tx.SourceRuntimeIntent.CreateBulk(builders...).
		OnConflictColumns(entintent.FieldSourceID).
		Update(func(update *ent.SourceRuntimeIntentUpsert) {
			update.AddDesiredRevision(1)
			update.SetUpdatedAt(now)
		}).
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("upsert source runtime intents %v: %w", sourceIDs, err)
	}
	rows, err := tx.SourceRuntimeIntent.Query().
		Where(entintent.SourceIDIn(sourceIDs...)).
		Order(entintent.BySourceID()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query advanced source runtime intents %v: %w", sourceIDs, err)
	}
	if len(rows) != len(sourceIDs) {
		return nil, fmt.Errorf("query advanced source runtime intents %v: got %d rows", sourceIDs, len(rows))
	}
	intents := make([]Intent, len(rows))
	for i, row := range rows {
		intents[i] = intentFromRow(row)
	}
	return intents, nil
}

func sortedUniqueSourceIDs(sourceIDs []int64) []int64 {
	if len(sourceIDs) == 0 {
		return nil
	}
	ids := append([]int64(nil), sourceIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := ids[:1]
	for _, sourceID := range ids[1:] {
		if sourceID != out[len(out)-1] {
			out = append(out, sourceID)
		}
	}
	return out
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

func (s *Service) markRevisionsApplied(ctx context.Context, intents []Intent) error {
	return s.markRevisions(ctx, intents, "", true)
}

func (s *Service) markRevisionsPending(ctx context.Context, intents []Intent, applyError string) error {
	return s.markRevisions(ctx, intents, sanitizeApplyError(applyError), false)
}

// markRevisions locks and re-checks the batch immediately before its one
// exact-revision-guarded write. A row deleted or superseded during runtime
// convergence is excluded without being recreated. The upsert update takes
// each row's revision from its own excluded row, so heterogeneous desired
// revisions remain one bounded database operation.
func (s *Service) markRevisions(ctx context.Context, intents []Intent, applyError string, applied bool) error {
	if len(intents) == 0 {
		return nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("sourcetransport mark runtime revisions: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	intents, err = lockExactPendingRevisions(ctx, tx, intents)
	if err != nil {
		return fmt.Errorf("sourcetransport mark runtime revisions: %w", err)
	}
	if len(intents) == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sourcetransport mark runtime revisions: commit empty batch: %w", err)
		}
		return nil
	}
	if err := writeRevisionMarks(ctx, tx, intents, applyError, applied); err != nil {
		return fmt.Errorf("sourcetransport mark runtime revisions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sourcetransport mark runtime revisions: commit: %w", err)
	}
	return nil
}

func lockExactPendingRevisions(ctx context.Context, tx *ent.Tx, intents []Intent) ([]Intent, error) {
	sourceIDs, requested := normalizeRevisionRequests(intents)
	rows, err := tx.SourceRuntimeIntent.Query().Where(
		entintent.SourceIDIn(sourceIDs...),
		predicate.SourceRuntimeIntent(func(selector *sql.Selector) { selector.ForUpdate() }),
	).Order(entintent.BySourceID()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock current intents: %w", err)
	}
	current := make([]Intent, len(rows))
	for i, row := range rows {
		current[i] = intentFromRow(row)
	}
	return exactPendingRevisions(current, requested), nil
}

func writeRevisionMarks(ctx context.Context, tx *ent.Tx, intents []Intent, applyError string, applied bool) error {
	now := time.Now()
	builders := make([]*ent.SourceRuntimeIntentCreate, 0, len(intents))
	for _, intent := range intents {
		builder := tx.SourceRuntimeIntent.Create().
			SetSourceID(intent.SourceID).
			SetDesiredRevision(intent.DesiredRevision).
			SetAppliedRevision(intent.AppliedRevision).
			SetLastApplyAttempt(now).
			SetLastApplyError(applyError).
			SetUpdatedAt(now)
		if applied {
			builder.SetAppliedRevision(intent.DesiredRevision)
		}
		builders = append(builders, builder)
	}
	exactCurrentPending := sql.ExprP(
		`"source_runtime_intents"."desired_revision" = "excluded"."desired_revision" AND ` +
			`"source_runtime_intents"."desired_revision" > "source_runtime_intents"."applied_revision"`,
	)
	upsert := tx.SourceRuntimeIntent.CreateBulk(builders...).OnConflict(
		sql.ConflictColumns(entintent.FieldSourceID),
		sql.UpdateWhere(exactCurrentPending),
	).Update(func(update *ent.SourceRuntimeIntentUpsert) {
		update.UpdateLastApplyAttempt()
		update.UpdateLastApplyError()
		update.UpdateUpdatedAt()
		if applied {
			update.UpdateAppliedRevision()
		}
	})
	if err := upsert.Exec(ctx); err != nil {
		return err
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

func (s *Service) loadIntents(ctx context.Context, sourceIDs []int64) ([]Intent, error) {
	if len(sourceIDs) == 0 {
		return []Intent{}, nil
	}
	rows, err := s.client.SourceRuntimeIntent.Query().
		Where(entintent.SourceIDIn(sourceIDs...)).
		Order(entintent.BySourceID()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query source runtime intents %v: %w", sourceIDs, err)
	}
	bySource := make(map[int64]Intent, len(rows))
	for _, row := range rows {
		bySource[row.SourceID] = intentFromRow(row)
	}
	intents := make([]Intent, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		intent, ok := bySource[sourceID]
		if !ok {
			intent = Intent{SourceID: sourceID}
		}
		intents = append(intents, intent)
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
