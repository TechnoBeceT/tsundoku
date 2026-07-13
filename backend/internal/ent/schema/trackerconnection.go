package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// TrackerConnection holds the schema definition for the TrackerConnection
// entity: the APP-WIDE per-account token store for a native tracker (AniList,
// MAL, Kitsu, MangaUpdates).
//
// It is deliberately NOT per-series — a tracker account (its OAuth/session token
// and account-level metadata) is a single fact about the whole install, mirroring
// Suwayomi's proven split between an account/token store and a per-series binding
// (see TrackBinding for the per-series × tracker half). At most one connected
// account per tracker (tracker_id is Unique).
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table and rows are
// created when the owner connects a tracker account.
type TrackerConnection struct {
	ent.Schema
}

// Fields of the TrackerConnection.
func (TrackerConnection) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// tracker_id is the stable numeric identity of the tracker, from the fixed
		// registry: MAL=1, AniList=2, Kitsu=3, MangaUpdates=7. Unique — at most one
		// connected account per tracker. The registry is shared with
		// TrackBinding.tracker_id.
		field.Int("tracker_id").Unique(),
		// access_token is the OAuth access / session token for this account.
		//
		// SECURITY: stored PLAINTEXT at rest under the single-owner homelab threat
		// model, matching Suwayomi's own tracker token storage — at-rest encryption
		// is a future hardening, not a v1 requirement. Marked .Sensitive() so the
		// token never leaks through the generated String()/log path or JSON
		// serialization (mirrors owner.password_hash); the at-rest storage and Go
		// read access are unaffected.
		field.String("access_token").Sensitive().Default(""),
		// refresh_token is the OAuth refresh token. MAL-only in practice: AniList
		// issues an implicit-flow token with no refresh token, so it stays "".
		// .Sensitive() for the same log/JSON-leak reason as access_token.
		field.String("refresh_token").Optional().Sensitive().Default(""),
		// token_type is the OAuth token type, almost always "Bearer".
		field.String("token_type").Default("Bearer"),
		// expires_at is the access-token expiry. Nil = unknown / no expiry (e.g.
		// AniList implicit tokens).
		field.Time("expires_at").Optional().Nillable(),
		// username is the account's tracker username (display / confirmation).
		field.String("username").Default(""),
		// score_format is the AniList account score format
		// (POINT_100/POINT_10/POINT_10_DECIMAL/POINT_5/POINT_3); "" for trackers
		// that do not expose one.
		field.String("score_format").Optional().Default(""),
		// token_expired is set when a token refresh returns 401, forcing the owner
		// to re-login before further sync.
		field.Bool("token_expired").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the TrackerConnection. None — a connection is an app-wide account
// record with no link to any Series (the per-series link lives on TrackBinding).
func (TrackerConnection) Edges() []ent.Edge {
	return nil
}
