// Package disabledsource owns the TSUNDOKU-SIDE per-source enable/disable flag —
// the "Configure" dialog's per-source Switch, which since QCAT-513 means a FULL
// TEMPORARY PAUSE of that source rather than merely hiding it from a picker.
//
// WHY TSUNDOKU-SIDE: the internal engine (Rensaio, via internal/sourceengine)
// has no server-side "disabled source" concept — sourceengine.Source carries
// only ID/Name/Lang. So Tsundoku persists the flag in its OWN Postgres (the
// DisabledSource entity, one row per disabled source id) and applies it itself.
// It is NOT engine topology and is deliberately never read or pushed by
// internal/enginetopo (seed + reconcile).
//
// WHAT A PAUSE DOES — three consumers, and the pause is only complete with all
// three (QCAT-513; the flag used to have only the first, which is why pausing a
// walled-off source did not stop it being hammered):
//
//   - internal/imports.excludedFromPicker — hides it from Discover/Search/Browse.
//   - internal/refresh — the discovery sweep skips its providers, so its feeds are
//     not re-polled.
//   - internal/chapter's candidate ranking, via internal/download — it is dropped
//     from RankedLiveCandidates, so nothing downloads or upgrades FROM it and the
//     next-best provider wins new chapters instead.
//
// WHAT A PAUSE DOES NOT DO: it deletes NOTHING (Rule 2) and re-ranks NOTHING
// (Rule 3). Already-downloaded chapters keep their files, their `downloaded`
// state and their `satisfied_by` source, so they stay readable throughout;
// SeriesProvider rows, their ProviderChapter feeds and every CBZ are untouched;
// and no importance value is written. Un-pausing restores the source on the next
// cycle from the very same rows.
//
// The pause is MANUAL-ONLY: there is no auto-expiry (an automatic re-enable into
// a still-broken source just restarts the churn). created_at is immutable so the
// UI can render "paused since <date>".
//
// KNOWN LIMIT: the flag is keyed by NUMERIC engine source id, so it addresses a
// source's LIVE provider rows. A DISK-ORIGIN provider stores a display NAME
// instead of an id and carries no source id at all, so it is outside the pause's
// reach — as it is already outside the refresh sweep's, for the same reason. The
// library merge machinery (library.HealDriftedProviders) folds such rows into
// their live twin, after which the pause covers them.
package disabledsource

import (
	"context"
	"fmt"
	"time"

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

// Disabled returns the set of currently-paused engine-host source ids (a row's
// presence = paused). An empty (non-nil) map means nothing is paused.
//
// It is read ONCE PER PASS by each consumer — once per picker call
// (internal/imports), once per Configure-dialog GET
// (internal/handler/extensions), once per refresh sweep (internal/refresh) and
// once per download / upgrade-detection pass (internal/download). Never once per
// chapter or per provider: those loops run library-wide, so a per-item read would
// be an N+1 on the hottest paths in the engine.
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

// DisabledSince returns each currently-paused engine-host source id mapped to
// the immutable instant its DisabledSource row was created — the "paused since"
// timestamp the UI renders next to the pause control. Absence from the map means
// the source is active (identical membership to Disabled, which returns only the
// bool set).
//
// Like Disabled it is read ONCE PER PASS, never per source — the only callers are
// the Configure-dialog GET and the enable-toggle write in
// internal/handler/extensions, both of which resolve the whole set in one query.
func (s *Service) DisabledSince(ctx context.Context) (map[int64]time.Time, error) {
	rows, err := s.client.DisabledSource.Query().
		Select(entdisabledsource.FieldSourceID, entdisabledsource.FieldCreatedAt).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("disabledsource: query disabled-since: %w", err)
	}
	out := make(map[int64]time.Time, len(rows))
	for _, r := range rows {
		out[r.SourceID] = r.CreatedAt
	}
	return out, nil
}

// SetEnabled sets a source's paused/active state, idempotently:
//   - enabled=false PAUSES it — creates the DisabledSource row if absent (a
//     re-pause of an already-paused source is a no-op).
//   - enabled=true resumes it — deletes the row if present (a resume of an
//     already-active source is a no-op).
//
// It never touches any other Tsundoku row: no SeriesProvider, no Chapter, no
// ProviderChapter, no file. The pause is expressed entirely by this one row's
// presence, and every consumer reads it (see the package doc comment) — which is
// what makes resuming a source a single delete with no state to unwind.
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
