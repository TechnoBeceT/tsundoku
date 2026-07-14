package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// PendingTrackPush holds the schema definition for the PendingTrackPush
// entity: the DURABLE, COALESCING retry queue for tracker-progress pushes
// that failed on their first attempt (spec/trackers-sync-phase4 §3). One row
// per TrackBinding — the unique track_binding_id is the coalescing key, so a
// newer, higher chapter push always supersedes an older pending one instead
// of piling up duplicate rows for the same binding.
//
// This entity is deliberately a plain, edge-less UUID reference to
// TrackBinding (no ent.Edge), mirroring SourceMetric's own denormalized
// design: the retry queue is throwaway bookkeeping, not part of the
// durable tracker-binding record itself (see spec §4 — "Retry queue is
// throwaway (DB-only; a wipe loses pending pushes, acceptable — they
// re-trigger on next read)").
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table.
type PendingTrackPush struct {
	ent.Schema
}

// Fields of the PendingTrackPush.
func (PendingTrackPush) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// track_binding_id is the TrackBinding this pending push targets. UNIQUE:
		// at most one pending row per binding — the coalescing key (see
		// internal/tracker/retry.Queue.Enqueue).
		field.UUID("track_binding_id", uuid.UUID{}).Unique(),
		// chapter is the HIGHEST pending local chapter number to push. A later
		// Enqueue call for the same binding only overwrites this when its chapter
		// is strictly higher (never regress the pending value either).
		field.Float("chapter"),
		// attempts counts how many times the worker has tried to push this row
		// and failed. Once attempts reaches the hard cap (3, Komikku parity) the
		// row is left in place as a tracking-health signal and never retried
		// again — see internal/tracker/retry.Queue.RunOnce's doc comment.
		field.Int("attempts").Default(0),
		// next_attempt_at is the backoff gate: this row is not due again until
		// now >= next_attempt_at. Nil means "never attempted yet" (due
		// immediately).
		field.Time("next_attempt_at").Optional().Nillable(),
		// last_error records the most recent push failure reason ("" before the
		// first failure).
		field.String("last_error").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the PendingTrackPush. None — see the type doc comment for why
// track_binding_id is a plain denormalized field rather than an ent.Edge.
func (PendingTrackPush) Edges() []ent.Edge {
	return nil
}
