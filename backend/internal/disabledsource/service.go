// Package disabledsource owns the TSUNDOKU-SIDE per-source enable/disable flag —
// the "Configure" dialog's per-language Switch that hides a source's language
// from the Discover/Search/Browse pickers.
//
// WHY TSUNDOKU-SIDE: the internal engine (Rensaio, via internal/sourceengine)
// has no server-side "disabled source" concept — sourceengine.Source carries
// only ID/Name/Lang. So Tsundoku persists the flag in its OWN Postgres (the
// DisabledSource entity, one row per disabled source id) and applies the filter
// itself (internal/imports.excludedFromPicker). This flag is a pure UI/picker
// preference: it is NOT engine topology and is deliberately never read or
// pushed by internal/enginetopo (seed + reconcile). Disabling a source only
// declutters the pickers; it never touches an already-adopted series (the
// refresh sweep iterates SeriesProvider rows, and direct-by-id adopt/browse
// bypass the picker filter).
package disabledsource

import (
	"context"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entdisabledsource "github.com/technobecet/tsundoku/internal/ent/disabledsource"
)

// Service reads and toggles the per-source disabled flag over the DisabledSource
// table. A row's presence means the source is disabled; absence means enabled.
type Service struct {
	client *ent.Client
}

// NewService constructs a Service over the given Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}

// Disabled returns the set of currently-disabled engine-host source ids (a row's
// presence = disabled). An empty (non-nil) map means nothing is disabled. Read
// once per picker call by internal/imports and once per Configure-dialog GET by
// internal/handler/extensions.
func (s *Service) Disabled(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.client.DisabledSource.Query().
		Select(entdisabledsource.FieldSourceID).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("disabledsource: query disabled sources: %w", err)
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.SourceID] = true
	}
	return out, nil
}

// SetEnabled sets a source's enable/disable state, idempotently:
//   - enabled=false disables it — creates the DisabledSource row if absent (a
//     re-disable of an already-disabled source is a no-op).
//   - enabled=true re-enables it — deletes the row if present (a re-enable of an
//     already-enabled source is a no-op).
//
// It never touches any other Tsundoku row — an adopted series keeps updating
// regardless (see the package doc comment).
func (s *Service) SetEnabled(ctx context.Context, sourceID int64, enabled bool) error {
	if enabled {
		return s.enable(ctx, sourceID)
	}
	return s.disable(ctx, sourceID)
}

// disable creates the DisabledSource row for sourceID, treating a lost unique
// race (the row was created concurrently) as success — the desired end-state is
// "a row exists", which is now true either way.
func (s *Service) disable(ctx context.Context, sourceID int64) error {
	err := s.client.DisabledSource.Create().SetSourceID(sourceID).Exec(ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return fmt.Errorf("disabledsource: disable source %d: %w", sourceID, err)
	}
	return nil
}

// enable deletes any DisabledSource row for sourceID. Deleting zero rows (the
// source was already enabled) is not an error — the delete is idempotent.
func (s *Service) enable(ctx context.Context, sourceID int64) error {
	if _, err := s.client.DisabledSource.Delete().
		Where(entdisabledsource.SourceID(sourceID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("disabledsource: enable source %d: %w", sourceID, err)
	}
	return nil
}
