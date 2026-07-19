package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SourceEvent holds the schema definition for the SourceEvent entity: an
// append-only audit-log row for a single source operation (a search, a chapter
// download, a refresh sweep, an anti-bot warm, or a circuit-breaker transition).
// It is the substrate of the Source Health Console — the read side aggregates
// these rows (via SQL, never an in-memory load of the whole table) into KPIs,
// per-source rollups, and timeline histograms, and answers forensic questions a
// rolling snapshot cannot ("failing since when", "search works but downloads
// fail", "show me the request that failed at 14:32").
//
// REDEFINED from a dead, never-written stub ({source, event_type(string),
// payload(string), created_at}) into the typed, indexed shape below. The stub was
// referenced nowhere outside generated ent, so its table is empty in every
// deployment and the redefinition loses no data. The two superseded columns
// (source / payload) are dropped after auto-migration by
// sourceevents.DropLegacyColumns (migrate.go; Ent's additive Schema.Create never
// drops a column).
//
// The row is written BEST-EFFORT and fire-and-forget by internal/sourceevents
// (mirroring internal/metrics' Recorder posture): a write failure is logged and
// swallowed so audit bookkeeping can never break or slow a download / search /
// refresh. Retention is bounded — a daily purge deletes rows older than the
// reporting.retention_days tunable (default 30).
type SourceEvent struct {
	ent.Schema
}

// Fields of the SourceEvent.
//
// source_key is the JOIN KEY across the whole source-health domain: it is the
// canonical physical-source NAME (download.canonicalSourceKey = TrimSpace of the
// display name, else the id) — the SAME key SourceCircuitState (the breaker) and
// the source-metrics screen use, so events, breaker state, and metrics all line
// up on one identity. It is the only key computable on every path: a
// disk-reconciled provider has no numeric source id.
func (SourceEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_key — canonical NAME; the join key (see the method doc comment).
		field.String("source_key"),
		// source_id is the numeric engine-host source id as a string ("" for a
		// disk-reconciled source, which has none).
		field.String("source_id").Default(""),
		// source_name is the human display name captured at write time.
		field.String("source_name").Default(""),
		// language is the source's language code ("" when unknown).
		field.String("language").Default(""),
		// event_type classifies the source operation this row records.
		field.Enum("event_type").
			Values("search", "download", "refresh", "warm", "breaker_trip", "breaker_reset"),
		// status is the operation's binary outcome. A breaker_trip is recorded as
		// failed (the source went down); a breaker_reset as success (it recovered
		// or was manually reset).
		field.Enum("status").Values("success", "failed"),
		// duration_ms is the operation's wall-clock duration in milliseconds (0 when
		// not timed — e.g. a breaker transition).
		field.Int64("duration_ms").Default(0),
		// error_message is the (truncated) failure reason; nil on success.
		field.String("error_message").Optional().Nillable(),
		// error_category is the errorclass.Classify bucket of error_message
		// (captcha / rate_limit / …); nil on success.
		field.String("error_category").Optional().Nillable(),
		// items_count is the operation's result cardinality where meaningful (search
		// hits, chapters ingested); nil when not applicable.
		field.Int("items_count").Optional().Nillable(),
		// metadata carries operation-specific context (keyword, url, chapter, series)
		// as a small JSON string map — never used for aggregation, only forensic
		// display in the single-event modal.
		field.JSON("metadata", map[string]string{}).Optional(),
		// created_at is the immutable event timestamp — the axis every aggregation
		// buckets and sorts on.
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the SourceEvent. None — an event is a denormalized log row keyed by
// source_key (a NAME that spans engine-host and disk-reconciled sources), with no
// FK to any Tsundoku row.
func (SourceEvent) Edges() []ent.Edge {
	return nil
}

// Indexes of the SourceEvent. Every read path filters and/or orders by
// created_at, usually within a source_key / event_type / status facet — so each
// facet is paired with created_at as a composite, plus a lone created_at index
// for the global feed + the retention purge's range delete.
func (SourceEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_key", "created_at"),
		index.Fields("event_type", "created_at"),
		index.Fields("status", "created_at"),
		index.Fields("created_at"),
	}
}
