package sourcetransport

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entintent "github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	entpolicy "github.com/technobecet/tsundoku/internal/ent/sourcetransportpolicy"
)

const maxApplyErrorLen = 512

func (s *Service) persistPatchTx(ctx context.Context, tx *ent.Tx, sourceID int64, patch Patch) error {
	if patch.ReuseBypassSession.Operation == PatchSet || patch.ImageConnectionMode.Operation == PatchSet {
		if err := s.upsertPatchTx(ctx, tx, sourceID, patch); err != nil {
			return err
		}
	} else {
		update := tx.SourceTransportPolicy.Update().Where(entpolicy.SourceID(sourceID))
		applyClearPatch(update, patch)
		if _, err := update.Save(ctx); err != nil {
			return fmt.Errorf("clear source %d transport policy: %w", sourceID, err)
		}
	}
	if _, err := tx.SourceTransportPolicy.Delete().Where(
		entpolicy.SourceID(sourceID),
		entpolicy.ReuseBypassSessionIsNil(),
		entpolicy.ImageConnectionModeIsNil(),
	).Exec(ctx); err != nil {
		return fmt.Errorf("delete empty source %d transport policy: %w", sourceID, err)
	}
	return nil
}

func (s *Service) upsertPatchTx(ctx context.Context, tx *ent.Tx, sourceID int64, patch Patch) error {
	now := time.Now()
	create := tx.SourceTransportPolicy.Create().SetSourceID(sourceID).SetUpdatedAt(now)
	if patch.ReuseBypassSession.Operation == PatchSet {
		create.SetReuseBypassSession(patch.ReuseBypassSession.Value)
	}
	if patch.ImageConnectionMode.Operation == PatchSet {
		create.SetImageConnectionMode(entpolicy.ImageConnectionMode(patch.ImageConnectionMode.Value))
	}
	if err := create.OnConflictColumns(entpolicy.FieldSourceID).Update(func(update *ent.SourceTransportPolicyUpsert) {
		applyUpsertPatch(update, patch)
		update.SetUpdatedAt(now)
	}).Exec(ctx); err != nil {
		return fmt.Errorf("upsert source %d transport policy: %w", sourceID, err)
	}
	return nil
}

func applyUpsertPatch(update *ent.SourceTransportPolicyUpsert, patch Patch) {
	switch patch.ReuseBypassSession.Operation {
	case PatchSet:
		update.SetReuseBypassSession(patch.ReuseBypassSession.Value)
	case PatchClear:
		update.ClearReuseBypassSession()
	}
	switch patch.ImageConnectionMode.Operation {
	case PatchSet:
		update.SetImageConnectionMode(entpolicy.ImageConnectionMode(patch.ImageConnectionMode.Value))
	case PatchClear:
		update.ClearImageConnectionMode()
	}
}

func applyClearPatch(update *ent.SourceTransportPolicyUpdate, patch Patch) {
	if patch.ReuseBypassSession.Operation == PatchClear {
		update.ClearReuseBypassSession()
	}
	if patch.ImageConnectionMode.Operation == PatchClear {
		update.ClearImageConnectionMode()
	}
}

func (s *Service) loadOverride(ctx context.Context, sourceID int64) (Override, error) {
	row, err := s.client.SourceTransportPolicy.Query().Where(entpolicy.SourceID(sourceID)).Only(ctx)
	if ent.IsNotFound(err) {
		return Override{}, nil
	}
	if err != nil {
		return Override{}, fmt.Errorf("query source %d transport policy: %w", sourceID, err)
	}
	return overrideFromRow(row), nil
}

func overrideFromRow(row *ent.SourceTransportPolicy) Override {
	override := Override{ReuseBypassSession: row.ReuseBypassSession}
	if row.ImageConnectionMode != nil {
		mode := ImageConnectionMode(*row.ImageConnectionMode)
		override.ImageConnectionMode = &mode
	}
	return override
}

func (s *Service) loadIntent(ctx context.Context, sourceID int64) (Intent, error) {
	row, err := s.client.SourceRuntimeIntent.Query().Where(entintent.SourceID(sourceID)).Only(ctx)
	if ent.IsNotFound(err) {
		return Intent{SourceID: sourceID}, nil
	}
	if err != nil {
		return Intent{}, fmt.Errorf("query source %d runtime intent: %w", sourceID, err)
	}
	return intentFromRow(row), nil
}

func intentFromRow(row *ent.SourceRuntimeIntent) Intent {
	return Intent{
		SourceID:         row.SourceID,
		DesiredRevision:  row.DesiredRevision,
		AppliedRevision:  row.AppliedRevision,
		LastApplyAttempt: row.LastApplyAttempt,
		LastApplyError:   row.LastApplyError,
	}
}

func applyPatch(stored Override, patch Patch) Override {
	result := stored
	switch patch.ReuseBypassSession.Operation {
	case PatchSet:
		value := patch.ReuseBypassSession.Value
		result.ReuseBypassSession = &value
	case PatchClear:
		result.ReuseBypassSession = nil
	}
	switch patch.ImageConnectionMode.Operation {
	case PatchSet:
		value := patch.ImageConnectionMode.Value
		result.ImageConnectionMode = &value
	case PatchClear:
		result.ImageConnectionMode = nil
	}
	return result
}

func sanitizeApplyError(message string) string {
	message = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(message)
	message = strings.TrimSpace(message)
	if len(message) > maxApplyErrorLen {
		return message[:maxApplyErrorLen]
	}
	return message
}
