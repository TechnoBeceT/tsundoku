package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/technobecet/tsundoku/internal/ent"
	entintent "github.com/technobecet/tsundoku/internal/ent/globalruntimeintent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

const (
	runtimeIntentScope   = "engine_config"
	maxRuntimeApplyError = 512
	metadataWriteTimeout = time.Second
)

// EnsureRuntimeIntent gives upgraded installations a durable first convergence
// revision when they already contain an engine-runtime setting written before
// GlobalRuntimeIntent existed. Fresh installations with no persisted runtime
// overrides remain row-free. The transaction and conflict-ignore make the
// backfill atomic and idempotent alongside ordinary SetMany revision advances.
func EnsureRuntimeIntent(ctx context.Context, client *ent.Client) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("settings.EnsureRuntimeIntent: begin tx: %w", err)
	}
	present, err := tx.Settings.Query().Where(entsettings.KeyIn(runtimeConfigKeys...)).Exist(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("settings.EnsureRuntimeIntent: query runtime settings: %w", err)
	}
	if present {
		if err := tx.GlobalRuntimeIntent.Create().
			SetScope(runtimeIntentScope).
			SetDesiredRevision(1).
			OnConflictColumns(entintent.FieldScope).
			Ignore().
			Exec(ctx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("settings.EnsureRuntimeIntent: create pending revision: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings.EnsureRuntimeIntent: commit: %w", err)
	}
	return nil
}

// RuntimeIntent loads the singleton global runtime revision. A missing row is
// the zero revision, which is already applied and needs no convergence.
func (s *Service) RuntimeIntent(ctx context.Context) (RuntimeIntent, error) {
	row, err := s.client.GlobalRuntimeIntent.Query().Where(entintent.Scope(runtimeIntentScope)).Only(ctx)
	if ent.IsNotFound(err) {
		return RuntimeIntent{}, nil
	}
	if err != nil {
		return RuntimeIntent{}, fmt.Errorf("settings.RuntimeIntent: query: %w", err)
	}
	return runtimeIntentFromRow(row), nil
}

// ApplyPending converges and conditionally acknowledges the current global
// runtime revision. Calls coalesce under a context-cancelable serializer. A
// revision committed during the external apply cannot be falsely acknowledged
// because the update is guarded by the exact attempted desired revision.
func (s *Service) ApplyPending(ctx context.Context) (RuntimeIntent, error) {
	if lifecycle, ok := s.runtimeConverger.(interface {
		RunRuntime(context.Context, func(context.Context) error) error
	}); ok {
		var intent RuntimeIntent
		err := lifecycle.RunRuntime(ctx, func(ctx context.Context) error {
			var applyErr error
			intent, applyErr = s.applyPending(ctx)
			return applyErr
		})
		return intent, err
	}
	return s.applyPending(ctx)
}

func (s *Service) applyPending(ctx context.Context) (RuntimeIntent, error) {
	if err := s.runtimeApplySem.Acquire(ctx, 1); err != nil {
		return RuntimeIntent{}, fmt.Errorf("settings.ApplyPending: acquire runtime apply: %w", err)
	}
	defer s.runtimeApplySem.Release(1)

	intent, err := s.RuntimeIntent(ctx)
	if err != nil {
		return RuntimeIntent{}, err
	}
	if intent.DesiredRevision <= intent.AppliedRevision {
		return intent, nil
	}
	if s.runtimeConverger == nil {
		return intent, errors.New("settings.ApplyPending: runtime converger is not configured")
	}

	attemptedRevision := intent.DesiredRevision
	if applyErr := s.runtimeConverger.ReconcileRuntime(ctx); applyErr != nil {
		metadataCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metadataWriteTimeout)
		markErr := s.markRuntimePending(metadataCtx, attemptedRevision, applyErr.Error())
		cancel()
		current, loadErr := s.RuntimeIntent(ctx)
		return current, errors.Join(applyErr, markErr, loadErr)
	}
	if err := s.markRuntimeApplied(ctx, attemptedRevision); err != nil {
		return RuntimeIntent{}, err
	}
	return s.RuntimeIntent(ctx)
}

// ReconcilePending performs one bounded retry of the singleton global intent.
// It is safe in installations with no source rows because it never synthesizes
// a source identity.
func (s *Service) ReconcilePending(ctx context.Context) error {
	_, err := s.ApplyPending(ctx)
	return err
}

func (s *Service) advanceRuntimeIntentTx(ctx context.Context, tx *ent.Tx) (RuntimeIntent, error) {
	now := time.Now()
	if err := tx.GlobalRuntimeIntent.Create().
		SetScope(runtimeIntentScope).
		SetDesiredRevision(1).
		OnConflictColumns(entintent.FieldScope).
		Update(func(update *ent.GlobalRuntimeIntentUpsert) {
			update.AddDesiredRevision(1)
			update.SetUpdatedAt(now)
		}).
		Exec(ctx); err != nil {
		return RuntimeIntent{}, fmt.Errorf("advance global runtime intent: %w", err)
	}
	row, err := tx.GlobalRuntimeIntent.Query().Where(entintent.Scope(runtimeIntentScope)).Only(ctx)
	if err != nil {
		return RuntimeIntent{}, fmt.Errorf("query advanced global runtime intent: %w", err)
	}
	return runtimeIntentFromRow(row), nil
}

func (s *Service) markRuntimeApplied(ctx context.Context, revision int64) error {
	_, err := s.client.GlobalRuntimeIntent.Update().Where(
		entintent.Scope(runtimeIntentScope),
		entintent.DesiredRevisionEQ(revision),
	).SetAppliedRevision(revision).SetLastApplyAttempt(time.Now()).SetLastApplyError("").Save(ctx)
	if err != nil {
		return fmt.Errorf("settings.markRuntimeApplied revision %d: %w", revision, err)
	}
	return nil
}

func (s *Service) markRuntimePending(ctx context.Context, revision int64, applyError string) error {
	_, err := s.client.GlobalRuntimeIntent.Update().Where(
		entintent.Scope(runtimeIntentScope),
		entintent.DesiredRevisionEQ(revision),
	).SetLastApplyAttempt(time.Now()).SetLastApplyError(sanitizeRuntimeApplyError(applyError)).Save(ctx)
	if err != nil {
		return fmt.Errorf("settings.markRuntimePending revision %d: %w", revision, err)
	}
	return nil
}

func runtimeIntentFromRow(row *ent.GlobalRuntimeIntent) RuntimeIntent {
	return RuntimeIntent{
		DesiredRevision:  row.DesiredRevision,
		AppliedRevision:  row.AppliedRevision,
		LastApplyAttempt: row.LastApplyAttempt,
		LastApplyError:   row.LastApplyError,
	}
}

func sanitizeRuntimeApplyError(message string) string {
	message = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(message)
	message = strings.TrimSpace(message)
	if len(message) <= maxRuntimeApplyError {
		return message
	}
	end := 0
	for _, r := range message {
		size := utf8.RuneLen(r)
		if end+size > maxRuntimeApplyError {
			break
		}
		end += size
	}
	return message[:end]
}
