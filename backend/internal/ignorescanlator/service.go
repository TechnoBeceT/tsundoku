// Package ignorescanlator owns the TSUNDOKU-SIDE per-source "ignore scanlator"
// flag — the "Configure" dialog's per-source toggle that collapses a source's
// per-uploader providers back into one [Source] provider.
//
// WHY IT EXISTS: some sources (the Iken multisrc template family, e.g. Hive
// Scans) put the chapter UPLOADER into the scanlator field rather than a real
// translation group. Tsundoku's scanlator-aware provider split then fragments
// such a source into fake per-uploader providers ([Hive Scans-Admin],
// [Hive Scans-Aero], …). Flagging the source ON tells Tsundoku to force that
// source's chapter scanlator to "" at every ingest/adopt choke point, so it
// collapses to a single [Source] provider and its filenames become [Source]
// naturally (see internal/ingest.Ingest.EffectiveScanlator).
//
// WHY TSUNDOKU-SIDE: the internal engine (Rensaio, via internal/sourceengine)
// has no server-side concept of this — it is a pure interpretation preference of
// Tsundoku's own scanlator-aware provider model. So Tsundoku persists the flag
// in its OWN Postgres (the IgnoreScanlatorSource entity, one row per flagged
// source id) and applies it at its ingest layer. It is NOT engine topology and
// is deliberately never read or pushed by internal/enginetopo (seed + reconcile).
//
// APPLY-FORWARD ONLY (Slice A): flagging a source affects only NEW
// ingests/adopts/breakdowns from now on. It never migrates already-adopted
// per-uploader SeriesProvider rows or existing CBZs — that is Slice B.
package ignorescanlator

import (
	"context"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entignorescanlatorsource "github.com/technobecet/tsundoku/internal/ent/ignorescanlatorsource"
)

// Service reads and toggles the per-source ignore-scanlator flag over the
// IgnoreScanlatorSource table. A row's presence means the flag is ON; absence
// means OFF (the default split-by-scanlator behaviour).
type Service struct {
	client *ent.Client
}

// NewService constructs a Service over the given Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}

// IgnoreScanlatorSet returns the set of engine-host source ids currently flagged
// ignore-scanlator (a row's presence = flagged), read in ONE query (no N+1). An
// empty (non-nil) map means nothing is flagged. Read once per ingest/adopt/
// breakdown operation by internal/ingest + internal/imports, and once per
// Configure-dialog GET by internal/handler/extensions.
func (s *Service) IgnoreScanlatorSet(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.client.IgnoreScanlatorSource.Query().
		Select(entignorescanlatorsource.FieldSourceID).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("ignorescanlator: query flagged sources: %w", err)
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.SourceID] = true
	}
	return out, nil
}

// SetIgnore sets a source's ignore-scanlator flag, idempotently:
//   - ignore=true flags it — creates the IgnoreScanlatorSource row if absent (a
//     re-flag of an already-flagged source is a no-op).
//   - ignore=false clears it — deletes the row if present (a clear of an
//     already-unflagged source is a no-op).
//
// It never touches any other Tsundoku row — an already-adopted series and its
// per-uploader providers keep updating regardless (Slice A is apply-forward
// only; see the package doc comment).
func (s *Service) SetIgnore(ctx context.Context, sourceID int64, ignore bool) error {
	if ignore {
		return s.flag(ctx, sourceID)
	}
	return s.unflag(ctx, sourceID)
}

// flag creates the IgnoreScanlatorSource row for sourceID, treating a lost
// unique race (the row was created concurrently) as success — the desired
// end-state is "a row exists", which is now true either way.
func (s *Service) flag(ctx context.Context, sourceID int64) error {
	err := s.client.IgnoreScanlatorSource.Create().SetSourceID(sourceID).Exec(ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return fmt.Errorf("ignorescanlator: flag source %d: %w", sourceID, err)
	}
	return nil
}

// unflag deletes any IgnoreScanlatorSource row for sourceID. Deleting zero rows
// (the source was already unflagged) is not an error — the delete is idempotent.
func (s *Service) unflag(ctx context.Context, sourceID int64) error {
	if _, err := s.client.IgnoreScanlatorSource.Delete().
		Where(entignorescanlatorsource.SourceID(sourceID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("ignorescanlator: unflag source %d: %w", sourceID, err)
	}
	return nil
}
