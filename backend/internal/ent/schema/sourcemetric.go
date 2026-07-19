package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceMetric holds the schema definition for the SourceMetric entity: a
// persisted, rolling per-source performance snapshot (one row per Suwayomi
// source). It backs the source-metrics API and the anti-bot warm-up job, which
// keeps slow (Cloudflare-protected) sources warm so interactive search stays
// fast.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: existing databases gain an empty table and rows are
// created lazily the first time a source is searched or warmed.
//
// NOTE: SourceMetric is a rolling SNAPSHOT (one row/source, EWMA + counters);
// the separate SourceEvent entity is the append-only per-operation AUDIT LOG
// (many rows/source) that backs the Source Health Console. They are distinct and
// must not be confused — a metric answers "how is this source doing lately", an
// event answers "what happened at 14:32".
type SourceMetric struct {
	ent.Schema
}

// Fields of the SourceMetric.
//
// "Slow" is DERIVED at read time (ewma_latency_ms > threshold), never stored as
// a flag, so the threshold can be re-tuned without a migration.
func (SourceMetric) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_id is the Suwayomi source id — one metric row per source.
		field.String("source_id").Unique(),
		// source_name is the denormalized display name, refreshed on each record.
		field.String("source_name").Default(""),
		// ewma_latency_ms is the exponentially-weighted rolling search latency in
		// milliseconds (a smooth "slow lately" signal without a time-series).
		field.Int("ewma_latency_ms").Default(0),
		// last_latency_ms is the most recent measured latency in milliseconds.
		field.Int("last_latency_ms").Default(0),
		// search_count / success_count / fail_count are lifetime counters.
		field.Int("search_count").Default(0),
		field.Int("success_count").Default(0),
		field.Int("fail_count").Default(0),
		// last_error is the most recent failure reason ("" when none).
		field.String("last_error").Default(""),
		// last_error_at / last_success_at / last_warmed_at are optional timestamps.
		field.Time("last_error_at").Optional().Nillable(),
		field.Time("last_success_at").Optional().Nillable(),
		field.Time("last_warmed_at").Optional().Nillable(),
		// updated_at is refreshed on every write.
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceMetric. None — a metric is keyed only by its Suwayomi
// source id (a denormalized snapshot, no FK to any Tsundoku row).
//
// No explicit Indexes(): source_id is Unique(), which already creates the unique
// index every lookup (recordOne / SetWarmed / Snapshot) uses.
func (SourceMetric) Edges() []ent.Edge {
	return nil
}
