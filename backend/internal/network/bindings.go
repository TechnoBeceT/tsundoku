package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entbinding "github.com/technobecet/tsundoku/internal/ent/sourcenetworkbinding"
	entintent "github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

// BindingMutationResult is the persisted binding state and exact source
// runtime revision committed by one PUT or DELETE. BindingDTO is empty for a
// successful delete. Changed is false only for an identical PUT.
type BindingMutationResult struct {
	BindingDTO
	Intent  sourcetransport.Intent
	Changed bool
}

// ListBindings returns every per-source binding (ordered by source id). An empty
// (non-nil) slice means no source has a non-default route — every source uses
// the global default. Drives the assignment table's GET /api/network/bindings.
func (s *Service) ListBindings(ctx context.Context) ([]BindingDTO, error) {
	rows, err := s.client.SourceNetworkBinding.Query().
		Order(ent.Asc(entbinding.FieldSourceID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("network.ListBindings: query: %w", err)
	}
	out := make([]BindingDTO, len(rows))
	for i, r := range rows {
		out[i] = newBindingDTO(r)
	}
	return out, nil
}

// GetBinding returns the binding for one source id, or ErrBindingNotFound
// (→404) when the source is unbound (uses the global default).
func (s *Service) GetBinding(ctx context.Context, sourceID int64) (BindingDTO, error) {
	row, err := s.bindingBySource(ctx, sourceID)
	if err != nil {
		return BindingDTO{}, err
	}
	return newBindingDTO(row), nil
}

// SetBinding upserts the binding for a source (source_id is unique — one binding
// per source). It validates the referenced endpoints exist and match the
// expected kind (a socks_endpoint_id must name a "socks" endpoint, a
// flare_endpoint_id a "flaresolverr" endpoint) and enforces the flare_mode ↔
// flare_endpoint_id consistency rule, then atomically creates or updates the
// row with its desired runtime revision. An identical value returns Changed
// false without advancing intent. ErrInvalidBinding (→400) on a bad reference
// or inconsistent mode.
func (s *Service) SetBinding(ctx context.Context, sourceID int64, in BindingInput) (BindingMutationResult, error) {
	if err := s.requireSource(ctx, sourceID); err != nil {
		return BindingMutationResult{}, err
	}
	if s.policyCoordinator == nil {
		return s.setBinding(ctx, sourceID, in)
	}
	var result BindingMutationResult
	err := s.mutate(ctx, func(context.Context) (runtimepolicy.Proposal, error) {
		return runtimepolicy.Proposal{Bindings: map[int64]*runtimepolicy.Binding{sourceID: {
			FlareMode: in.FlareMode, FlareEndpointID: in.FlareEndpointID, SocksEndpointID: in.SocksEndpointID,
		}}}, nil
	}, func(ctx context.Context) error {
		var err error
		result, err = s.setBinding(ctx, sourceID, in)
		return err
	})
	if errors.Is(err, runtimepolicy.ErrInvalidSelection) {
		return BindingMutationResult{}, fmt.Errorf("%w: %w", ErrInvalidBinding, err)
	}
	if sanitized := sanitizeKCEFInvariantError(err, ErrInvalidBinding); sanitized != nil {
		return BindingMutationResult{}, sanitized
	}
	return result, err
}

func (s *Service) setBinding(ctx context.Context, sourceID int64, in BindingInput) (BindingMutationResult, error) {
	if err := s.validateBindingInput(ctx, in); err != nil {
		return BindingMutationResult{}, err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return BindingMutationResult{}, fmt.Errorf("network.SetBinding: begin transaction: %w", err)
	}
	txService := *s
	txService.client = tx.Client()
	existing, existingErr := txService.bindingBySource(ctx, sourceID)
	if unchanged := existingErr == nil && bindingMatchesInput(existing, in); unchanged {
		return s.commitUnchangedBinding(ctx, tx, sourceID, existing)
	}
	if err := rejectUnexpectedBindingLookupError(existingErr); err != nil {
		_ = tx.Rollback()
		return BindingMutationResult{}, err
	}
	if err := txService.upsertBinding(ctx, sourceID, in); err != nil {
		_ = tx.Rollback()
		return BindingMutationResult{}, err
	}
	intent, err := s.runtimeIntent.AdvanceIntentTx(ctx, tx, sourceID)
	if err != nil {
		_ = tx.Rollback()
		return BindingMutationResult{}, fmt.Errorf("network.SetBinding: advance source %d intent: %w", sourceID, err)
	}
	if err := tx.Commit(); err != nil {
		return BindingMutationResult{}, fmt.Errorf("network.SetBinding: commit source %d: %w", sourceID, err)
	}
	binding, err := s.GetBinding(ctx, sourceID)
	if err != nil {
		return BindingMutationResult{}, err
	}
	return BindingMutationResult{BindingDTO: binding, Intent: intent, Changed: true}, nil
}

func rejectUnexpectedBindingLookupError(err error) error {
	if err == nil || errors.Is(err, ErrBindingNotFound) {
		return nil
	}
	return err
}

func (s *Service) validateBindingInput(ctx context.Context, in BindingInput) error {
	if err := validateFlareMode(in.FlareMode, in.FlareEndpointID); err != nil {
		return err
	}
	if err := s.validateEndpointRef(ctx, in.SocksEndpointID, KindSocks); err != nil {
		return err
	}
	return s.validateEndpointRef(ctx, in.FlareEndpointID, KindFlareSolverr)
}

func (s *Service) commitUnchangedBinding(ctx context.Context, tx *ent.Tx, sourceID int64, existing *ent.SourceNetworkBinding) (BindingMutationResult, error) {
	out := newBindingDTO(existing)
	if err := tx.Commit(); err != nil {
		return BindingMutationResult{}, fmt.Errorf("network.SetBinding: commit unchanged source %d: %w", sourceID, err)
	}
	intent, err := s.currentIntent(ctx, sourceID)
	if err != nil {
		return BindingMutationResult{}, err
	}
	return BindingMutationResult{BindingDTO: out, Intent: intent}, nil
}

// ClearBinding removes a source's binding and advances its desired runtime
// revision in one transaction, reverting it to the global default.
// ErrBindingNotFound (→404) is a no-op when no binding exists.
func (s *Service) ClearBinding(ctx context.Context, sourceID int64) (BindingMutationResult, error) {
	if err := s.requireSource(ctx, sourceID); err != nil {
		return BindingMutationResult{}, err
	}
	if s.policyCoordinator != nil {
		var result BindingMutationResult
		err := s.mutate(ctx, func(context.Context) (runtimepolicy.Proposal, error) {
			return runtimepolicy.Proposal{Bindings: map[int64]*runtimepolicy.Binding{sourceID: nil}}, nil
		}, func(ctx context.Context) error {
			var err error
			result, err = s.clearBinding(ctx, sourceID)
			return err
		})
		return result, err
	}
	return s.clearBinding(ctx, sourceID)
}

func (s *Service) requireSource(ctx context.Context, sourceID int64) error {
	if s.sourceCatalog == nil {
		return nil
	}
	if err := s.sourceCatalog.RequireSource(ctx, sourceID); err != nil {
		return fmt.Errorf("network binding source %d: %w", sourceID, err)
	}
	return nil
}

func (s *Service) clearBinding(ctx context.Context, sourceID int64) (BindingMutationResult, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return BindingMutationResult{}, fmt.Errorf("network.ClearBinding: begin transaction: %w", err)
	}
	n, err := tx.SourceNetworkBinding.Delete().
		Where(entbinding.SourceID(sourceID)).
		Exec(ctx)
	if err != nil {
		_ = tx.Rollback()
		return BindingMutationResult{}, fmt.Errorf("network.ClearBinding: delete source %d: %w", sourceID, err)
	}
	if n == 0 {
		_ = tx.Rollback()
		return BindingMutationResult{}, ErrBindingNotFound
	}
	intent, err := s.runtimeIntent.AdvanceIntentTx(ctx, tx, sourceID)
	if err != nil {
		_ = tx.Rollback()
		return BindingMutationResult{}, fmt.Errorf("network.ClearBinding: advance source %d intent: %w", sourceID, err)
	}
	if err := tx.Commit(); err != nil {
		return BindingMutationResult{}, fmt.Errorf("network.ClearBinding: commit source %d: %w", sourceID, err)
	}
	return BindingMutationResult{Intent: intent, Changed: true}, nil
}

func (s *Service) currentIntent(ctx context.Context, sourceID int64) (sourcetransport.Intent, error) {
	row, err := s.client.SourceRuntimeIntent.Query().Where(entintent.SourceID(sourceID)).Only(ctx)
	if ent.IsNotFound(err) {
		return sourcetransport.Intent{SourceID: sourceID}, nil
	}
	if err != nil {
		return sourcetransport.Intent{}, fmt.Errorf("network binding source %d runtime intent: %w", sourceID, err)
	}
	return sourcetransport.Intent{
		SourceID: row.SourceID, DesiredRevision: row.DesiredRevision, AppliedRevision: row.AppliedRevision,
		LastApplyAttempt: row.LastApplyAttempt, LastApplyError: row.LastApplyError,
	}, nil
}

func bindingMatchesInput(row *ent.SourceNetworkBinding, in BindingInput) bool {
	return uuidPointersEqual(row.SocksEndpointID, in.SocksEndpointID) &&
		row.FlareMode == in.FlareMode &&
		uuidPointersEqual(row.FlareEndpointID, in.FlareEndpointID)
}

func uuidPointersEqual(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// upsertBinding creates or updates the single binding row for sourceID from in.
func (s *Service) upsertBinding(ctx context.Context, sourceID int64, in BindingInput) error {
	existing, err := s.bindingBySource(ctx, sourceID)
	switch {
	case err == nil:
		upd := s.client.SourceNetworkBinding.UpdateOneID(existing.ID).SetFlareMode(in.FlareMode)
		// A nil reference must CLEAR the column (SetNillable is a no-op on nil),
		// so branch explicitly per dimension.
		if in.SocksEndpointID != nil {
			upd.SetSocksEndpointID(*in.SocksEndpointID)
		} else {
			upd.ClearSocksEndpointID()
		}
		if in.FlareEndpointID != nil {
			upd.SetFlareEndpointID(*in.FlareEndpointID)
		} else {
			upd.ClearFlareEndpointID()
		}
		if _, uErr := upd.Save(ctx); uErr != nil {
			return fmt.Errorf("network.SetBinding: update source %d: %w", sourceID, uErr)
		}
		return nil
	case errors.Is(err, ErrBindingNotFound):
		_, cErr := s.client.SourceNetworkBinding.Create().
			SetSourceID(sourceID).
			SetNillableSocksEndpointID(in.SocksEndpointID).
			SetFlareMode(in.FlareMode).
			SetNillableFlareEndpointID(in.FlareEndpointID).
			Save(ctx)
		if cErr != nil {
			return fmt.Errorf("network.SetBinding: create source %d: %w", sourceID, cErr)
		}
		return nil
	default:
		return err
	}
}

// validateEndpointRef confirms an optional endpoint reference points at an
// existing endpoint of the expected kind. A nil reference is valid (no override
// for that dimension). Returns ErrInvalidBinding (→400) when the endpoint is
// missing or the wrong kind.
func (s *Service) validateEndpointRef(ctx context.Context, id *uuid.UUID, wantKind string) error {
	if id == nil {
		return nil
	}
	row, err := s.client.NetworkEndpoint.Get(ctx, *id)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("%w: %s endpoint %s does not exist", ErrInvalidBinding, wantKind, *id)
		}
		return fmt.Errorf("network.validateEndpointRef: get %s: %w", *id, err)
	}
	if row.Kind != wantKind {
		return fmt.Errorf("%w: endpoint %s is kind %q, expected %q", ErrInvalidBinding, *id, row.Kind, wantKind)
	}
	return nil
}

// bindingBySource loads one binding by source id, translating a not-found into
// ErrBindingNotFound.
func (s *Service) bindingBySource(ctx context.Context, sourceID int64) (*ent.SourceNetworkBinding, error) {
	row, err := s.client.SourceNetworkBinding.Query().
		Where(entbinding.SourceID(sourceID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrBindingNotFound
		}
		return nil, fmt.Errorf("network.bindingBySource: query source %d: %w", sourceID, err)
	}
	return row, nil
}
