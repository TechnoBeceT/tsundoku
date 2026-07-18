package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entbinding "github.com/technobecet/tsundoku/internal/ent/sourcenetworkbinding"
)

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
// flare_endpoint_id consistency rule, then creates or updates the row and
// returns the persisted DTO (§16 round-trip). ErrInvalidBinding (→400) on a bad
// reference or inconsistent mode.
func (s *Service) SetBinding(ctx context.Context, sourceID int64, in BindingInput) (BindingDTO, error) {
	if err := validateFlareMode(in.FlareMode, in.FlareEndpointID); err != nil {
		return BindingDTO{}, err
	}
	if err := s.validateEndpointRef(ctx, in.SocksEndpointID, KindSocks); err != nil {
		return BindingDTO{}, err
	}
	if err := s.validateEndpointRef(ctx, in.FlareEndpointID, KindFlareSolverr); err != nil {
		return BindingDTO{}, err
	}

	if err := s.upsertBinding(ctx, sourceID, in); err != nil {
		return BindingDTO{}, err
	}
	return s.GetBinding(ctx, sourceID)
}

// ClearBinding removes a source's binding, reverting it to the global default.
// ErrBindingNotFound (→404) when the source had no binding to clear.
func (s *Service) ClearBinding(ctx context.Context, sourceID int64) error {
	n, err := s.client.SourceNetworkBinding.Delete().
		Where(entbinding.SourceID(sourceID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("network.ClearBinding: delete source %d: %w", sourceID, err)
	}
	if n == 0 {
		return ErrBindingNotFound
	}
	return nil
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
