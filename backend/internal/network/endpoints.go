package network

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entendpoint "github.com/technobecet/tsundoku/internal/ent/networkendpoint"
	entbinding "github.com/technobecet/tsundoku/internal/ent/sourcenetworkbinding"
)

// ListEndpoints returns every network endpoint ordered by name (passwords
// omitted). An empty (non-nil) slice means the owner has defined none.
func (s *Service) ListEndpoints(ctx context.Context) ([]EndpointDTO, error) {
	rows, err := s.client.NetworkEndpoint.Query().
		Order(ent.Asc(entendpoint.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("network.ListEndpoints: query: %w", err)
	}
	out := make([]EndpointDTO, len(rows))
	for i, r := range rows {
		out[i] = newEndpointDTO(r)
	}
	return out, nil
}

// CreateEndpoint validates in by kind and creates the endpoint, returning the
// persisted DTO (§16 round-trip). ErrInvalidEndpoint (→400) on bad fields.
func (s *Service) CreateEndpoint(ctx context.Context, in EndpointInput) (EndpointDTO, error) {
	if err := validateEndpoint(in); err != nil {
		return EndpointDTO{}, err
	}
	row, err := s.client.NetworkEndpoint.Create().
		SetName(in.Name).
		SetKind(in.Kind).
		SetEnabled(in.Enabled).
		SetHost(in.Host).
		SetPort(in.Port).
		SetSocksVersion(in.SocksVersion).
		SetUsername(in.Username).
		SetPassword(in.Password).
		SetURL(in.URL).
		SetSession(in.Session).
		SetSessionTTL(in.SessionTTL).
		SetTimeout(in.Timeout).
		SetAsResponseFallback(in.AsResponseFallback).
		Save(ctx)
	if err != nil {
		return EndpointDTO{}, fmt.Errorf("network.CreateEndpoint: save: %w", err)
	}
	return newEndpointDTO(row), nil
}

// UpdateEndpoint applies a partial patch to an existing endpoint. It loads the
// row, overlays every non-nil patch field, RE-VALIDATES the resulting endpoint
// by its (possibly changed) kind, and saves — so the store never holds an
// invalid endpoint. A nil Password keeps the stored password (write-only).
// ErrEndpointNotFound (→404) on a missing id; ErrInvalidEndpoint (→400) on bad
// merged fields.
func (s *Service) UpdateEndpoint(ctx context.Context, id uuid.UUID, patch EndpointPatch) (EndpointDTO, error) {
	row, err := s.endpointByID(ctx, id)
	if err != nil {
		return EndpointDTO{}, err
	}

	merged := applyPatch(row, patch)
	if err := validateEndpoint(merged); err != nil {
		return EndpointDTO{}, err
	}

	upd := s.client.NetworkEndpoint.UpdateOneID(id).
		SetName(merged.Name).
		SetKind(merged.Kind).
		SetEnabled(merged.Enabled).
		SetHost(merged.Host).
		SetPort(merged.Port).
		SetSocksVersion(merged.SocksVersion).
		SetUsername(merged.Username).
		SetURL(merged.URL).
		SetSession(merged.Session).
		SetSessionTTL(merged.SessionTTL).
		SetTimeout(merged.Timeout).
		SetAsResponseFallback(merged.AsResponseFallback)
	// Only touch the password when the patch explicitly carried one (write-only).
	if patch.Password != nil {
		upd = upd.SetPassword(*patch.Password)
	}
	saved, err := upd.Save(ctx)
	if err != nil {
		return EndpointDTO{}, fmt.Errorf("network.UpdateEndpoint: save %s: %w", id, err)
	}
	return newEndpointDTO(saved), nil
}

// DeleteEndpoint removes an endpoint, but ONLY when no binding references it:
// if any binding names it (as its SOCKS or FlareSolverr endpoint) the delete is
// blocked with ErrEndpointInUse (→409, owner-safety bias) whose message lists
// the referencing source ids. ErrEndpointNotFound (→404) on a missing id.
func (s *Service) DeleteEndpoint(ctx context.Context, id uuid.UUID) error {
	if _, err := s.endpointByID(ctx, id); err != nil {
		return err
	}

	refs, err := s.referencingSources(ctx, id)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return fmt.Errorf("%w: referenced by source ids %v", ErrEndpointInUse, refs)
	}

	if err := s.client.NetworkEndpoint.DeleteOneID(id).Exec(ctx); err != nil {
		// Defensive: the row existed (confirmed above); an error here is a
		// DB-level failure.
		return fmt.Errorf("network.DeleteEndpoint: delete %s: %w", id, err)
	}
	return nil
}

// referencingSources returns the sorted source ids of every binding that names
// endpoint id (as its SOCKS or its FlareSolverr endpoint).
func (s *Service) referencingSources(ctx context.Context, id uuid.UUID) ([]int64, error) {
	rows, err := s.client.SourceNetworkBinding.Query().
		Where(entbinding.Or(
			entbinding.SocksEndpointID(id),
			entbinding.FlareEndpointID(id),
		)).
		Select(entbinding.FieldSourceID).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("network.referencingSources: query %s: %w", id, err)
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.SourceID
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids, nil
}

// endpointByID loads one endpoint, translating a not-found into
// ErrEndpointNotFound.
func (s *Service) endpointByID(ctx context.Context, id uuid.UUID) (*ent.NetworkEndpoint, error) {
	row, err := s.client.NetworkEndpoint.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrEndpointNotFound
		}
		return nil, fmt.Errorf("network.endpointByID: get %s: %w", id, err)
	}
	return row, nil
}

// applyPatch overlays a patch's non-nil fields onto a stored endpoint's values,
// returning the fully-resolved field set to validate + persist. The password is
// carried through from the row so the merged value is complete, but the actual
// write is gated on patch.Password (see UpdateEndpoint).
func applyPatch(row *ent.NetworkEndpoint, patch EndpointPatch) EndpointInput {
	merged := EndpointInput{
		Name:               row.Name,
		Kind:               row.Kind,
		Enabled:            row.Enabled,
		Host:               row.Host,
		Port:               row.Port,
		SocksVersion:       row.SocksVersion,
		Username:           row.Username,
		Password:           row.Password,
		URL:                row.URL,
		Session:            row.Session,
		SessionTTL:         row.SessionTTL,
		Timeout:            row.Timeout,
		AsResponseFallback: row.AsResponseFallback,
	}
	overlayStrings(&merged, patch)
	overlayScalars(&merged, patch)
	return merged
}

// overlayStrings applies the string-typed patch fields.
func overlayStrings(merged *EndpointInput, patch EndpointPatch) {
	if patch.Name != nil {
		merged.Name = *patch.Name
	}
	if patch.Kind != nil {
		merged.Kind = *patch.Kind
	}
	if patch.Username != nil {
		merged.Username = *patch.Username
	}
	if patch.Password != nil {
		merged.Password = *patch.Password
	}
	if patch.Host != nil {
		merged.Host = *patch.Host
	}
	if patch.URL != nil {
		merged.URL = *patch.URL
	}
	if patch.Session != nil {
		merged.Session = *patch.Session
	}
}

// overlayScalars applies the bool/int-typed patch fields.
func overlayScalars(merged *EndpointInput, patch EndpointPatch) {
	if patch.Enabled != nil {
		merged.Enabled = *patch.Enabled
	}
	if patch.Port != nil {
		merged.Port = *patch.Port
	}
	if patch.SocksVersion != nil {
		merged.SocksVersion = *patch.SocksVersion
	}
	if patch.SessionTTL != nil {
		merged.SessionTTL = *patch.SessionTTL
	}
	if patch.Timeout != nil {
		merged.Timeout = *patch.Timeout
	}
	if patch.AsResponseFallback != nil {
		merged.AsResponseFallback = *patch.AsResponseFallback
	}
}
