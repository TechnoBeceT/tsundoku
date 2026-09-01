package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SourceCoverage is a PERSISTED per-scanlator coverage snapshot for one
// (source, manga URL) pair — the answer to "which scanlator has which chapters,
// and how many" (GAP-140).
//
// It exists because computing it is expensive and getting more so: since a
// source moved behind JS Detection, every chapter-list page costs one WebView
// navigation, and a 1,301-chapter series needs ~330 of them (~15-20 minutes).
// The in-memory ingest.ChapterCache cannot carry that — it is lost on restart,
// and it stores only SUCCESSFUL fetches, so a timed-out walk cached nothing and
// every retry started from zero.
//
// It is a CACHE, not a feed: it never participates in chapter identity (Rule 1)
// and rows are OVERWRITTEN in place, never deleted (Rule 2). computed_at is
// surfaced to the owner as an explicit "as of" so a stale snapshot can never
// masquerade as fresh.
type SourceCoverage struct {
	ent.Schema
}

// Fields of the SourceCoverage.
func (SourceCoverage) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// The engine source id, as a string for the same reason the wire uses
		// one: it is a 64-bit id and JSON numbers are lossy above 2^53.
		field.String("source_id"),
		// The exact source-owned serialized manga address — the other half of
		// the identity. Its shape may be relative, opaque, or absolute.
		field.String("manga_url"),
		// The rendered SourceBreakdownDTO as JSON. Stored whole rather than
		// normalised into rows: it is a snapshot to hand back verbatim, and
		// nothing queries inside it.
		field.Text("payload").Default(""),
		// pending | ready | failed. A row is written as `pending` the moment a
		// job starts, so a concurrent request can tell "being computed" from
		// "never computed" without a second table — and imports.Coverage acts
		// on that: it serves a live `pending` row as-is instead of launching a
		// second ~20-minute walk for the same pair.
		field.String("status").Default("pending"),
		// When the PAYLOAD was produced — the "as of" the UI renders. Cleared
		// whenever there is no payload, i.e. while pending and on a failure.
		field.Time("computed_at").Optional().Nillable(),
		// When the ROW last changed. Distinct from computed_at, and the column
		// imports.Coverage's admission rules measure against: how long a
		// `pending` row has claimed a walk is running (past a bound it is a
		// dead process's leftover and is retried) and how long ago a `failed`
		// one failed (inside its cooldown it is served as a failure rather
		// than recomputed). computed_at cannot serve that — it is nil in both
		// of those states by design.
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		// The failure text when status is `failed`, so the owner sees WHY
		// rather than an empty panel.
		field.String("last_error").Default(""),
	}
}

// Indexes of the SourceCoverage.
func (SourceCoverage) Indexes() []ent.Index {
	return []ent.Index{
		// One snapshot per (source, manga). Structural dedup, mirroring how
		// Chapter and ProviderChapter enforce identity with a UNIQUE index
		// rather than application-layer checks.
		index.Fields("source_id", "manga_url").Unique(),
	}
}

// Edges of the SourceCoverage.
func (SourceCoverage) Edges() []ent.Edge {
	return nil
}
