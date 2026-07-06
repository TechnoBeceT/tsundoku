package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceCircuitState holds the schema definition for the SourceCircuitState
// entity: a persisted per-physical-source circuit-breaker (part of the
// source-politeness feature — see internal/sourcegate). It exists to stop
// Tsundoku's background source access (download, refresh, warm-up) from
// getting a source's IP Cloudflare-blocked: after enough consecutive
// failures a source is "tripped" and excluded from further access until its
// cooldown elapses.
//
// This is a NEW, separate entity from SourceMetric (search-performance
// snapshot) because the two are keyed differently: SourceMetric is keyed by
// the numeric Suwayomi source_id, but the gate must also cover
// disk-reconciled providers, which have NO numeric id — only a name. Reusing
// SourceMetric's key would be a mismatch; SourceCircuitState is keyed by the
// physical-source NAME instead (see download.canonicalSourceKey).
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: existing databases gain an empty table and rows are
// created lazily the first time a source's breaker state changes.
type SourceCircuitState struct {
	ent.Schema
}

// Fields of the SourceCircuitState.
func (SourceCircuitState) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_key is the physical-source identity: the trimmed source NAME
		// (download.canonicalSourceKey / TrimSpace(Source.Name)), NOT a numeric
		// Suwayomi source id — disk-reconciled providers have none, so the name
		// is the only key computable on every gated path (download, refresh,
		// warm-up).
		field.String("source_key").Unique(),
		// consecutive_failures counts failures since the last success. It resets
		// to 0 on RecordSuccess and drives the trip decision in RecordFailure
		// (tripped once it reaches the runtime-tunable failure threshold).
		field.Int("consecutive_failures").Default(0),
		// cooldown_until is set when the breaker trips (now + the runtime-tunable
		// cooldown) and cleared on RecordSuccess. Nil means "not tripped" —
		// IsAvailable treats a nil or past cooldown_until as available.
		field.Time("cooldown_until").Optional().Nillable(),
		// last_error is the most recent failure reason ("" when none), kept for
		// operator visibility (mirrors SourceMetric.last_error).
		field.String("last_error").Default(""),
		// updated_at is refreshed on every write.
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceCircuitState. None — the breaker is keyed only by the
// physical-source name (a denormalized snapshot, no FK to any Tsundoku row;
// the name spans both Suwayomi-adopted and disk-reconciled providers).
//
// No explicit Indexes(): source_key is Unique(), which already creates the
// unique index every lookup (IsAvailable / RecordSuccess / RecordFailure) uses.
func (SourceCircuitState) Edges() []ent.Edge {
	return nil
}
