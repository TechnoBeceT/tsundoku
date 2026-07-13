package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// TrackBinding holds the schema definition for the TrackBinding entity: the
// PER-SERIES × TRACKER binding that links one Tsundoku Series to one entry on a
// native tracker (AniList, MAL, Kitsu, MangaUpdates), carrying the remote link
// and reading progress.
//
// This is the per-series half of the tracker split (see TrackerConnection for the
// app-wide account/token half). At most one binding per (series, tracker) — the
// unique index on (series_id, tracker_id) enforces it — so a series may be bound
// to several DIFFERENT trackers but never twice on the same one.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table.
type TrackBinding struct {
	ent.Schema
}

// Fields of the TrackBinding.
func (TrackBinding) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// series_id is the FK backing the required series edge below.
		field.UUID("series_id", uuid.UUID{}),
		// tracker_id is the tracker's numeric identity, from the same registry as
		// TrackerConnection.tracker_id (MAL=1, AniList=2, Kitsu=3, MangaUpdates=7).
		field.Int("tracker_id"),
		// remote_id is the tracker's manga id — numeric-as-string for AniList/MAL,
		// a string in general (some trackers use non-numeric ids).
		field.String("remote_id"),
		// remote_url is the canonical link to the remote entry.
		field.String("remote_url").Default(""),
		// library_id is the AniList MediaList entry id, needed to UPDATE progress on
		// AniList (distinct from the media/manga remote_id). "" when N/A.
		field.String("library_id").Optional().Default(""),
		// title is the remote entry's title (display / confirmation of the binding).
		field.String("title").Default(""),
		// status is the tracker's native status code (per-tracker vocabulary).
		field.String("status").Default(""),
		// last_chapter_read is the furthest chapter read. Stored as a FLOAT so
		// fractional chapters survive; truncation to an integer (for trackers that
		// require it) happens only on push, later.
		field.Float("last_chapter_read").Default(0),
		// total_chapters is the remote total (0 = unknown / ongoing ⇒ later logic
		// must never auto-complete a series from a 0 here).
		field.Int("total_chapters").Default(0),
		// score is the reading score on the tracker's native scale.
		field.Float("score").Default(0),
		// start_date / finish_date are the reading dates on the remote entry; nil
		// when unset.
		field.Time("start_date").Optional().Nillable(),
		field.Time("finish_date").Optional().Nillable(),
		// private marks the remote entry as private on the tracker.
		field.Bool("private").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the TrackBinding.
func (TrackBinding) Edges() []ent.Edge {
	return []ent.Edge{
		// series is the required link to the bound Series, storing the FK in
		// series_id (inverse of Series.track_bindings). Unique — a binding belongs
		// to exactly one series.
		edge.From("series", Series.Type).
			Ref("track_bindings").
			Field("series_id").
			Unique().
			Required(),
	}
}

// Indexes of the TrackBinding.
func (TrackBinding) Indexes() []ent.Index {
	return []ent.Index{
		// At most one binding per (series, tracker): a series can be tracked on
		// several different trackers, but never twice on the same one.
		index.Fields("series_id", "tracker_id").Unique(),
	}
}
