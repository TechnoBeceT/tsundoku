package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// NetworkEndpoint holds the schema definition for the NetworkEndpoint entity: a
// named, reusable network-egress endpoint the owner defines once and then binds
// to individual sources (per-source network routing, QCAT-283). An endpoint is
// one of two KINDS, distinguished by the kind field:
//
//   - "socks"        — a SOCKS4/5 proxy the bound source's traffic egresses
//     through (the host/port/socks_version/username/password fields apply).
//   - "flaresolverr" — a FlareSolverr instance the bound source's Cloudflare
//     challenge-solving is routed to (the url/session/session_ttl/timeout/
//     as_response_fallback fields apply).
//
// Both field-groups live on the ONE table (the two are mutually exclusive per
// row and validated by kind in internal/network — Ent has no per-kind field
// constraint), mirroring how SourcePreference carries a typed value in a single
// string column. This entity is DB-truth only for THIS slice: the engine push
// that makes a binding actually route traffic is a later slice and never reads
// or writes these rows here.
//
// Every field is additive/defaulted, so adding this entity is a zero-data
// migration: Ent auto-migrate creates the empty table and endpoints are created
// lazily the first time the owner defines one.
type NetworkEndpoint struct {
	ent.Schema
}

// Fields of the NetworkEndpoint.
func (NetworkEndpoint) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// name is the owner-facing label for the endpoint (non-blank, enforced in
		// the service).
		field.String("name"),
		// kind selects which field-group applies: "socks" or "flaresolverr"
		// (validated in internal/network — Ent stores it as a plain string).
		field.String("kind"),
		// enabled toggles whether a binding to this endpoint takes effect; a
		// disabled endpoint is kept but not routed to.
		field.Bool("enabled").Default(true),

		// --- SOCKS field-group (kind == "socks") ---
		// host is the SOCKS proxy host (non-blank for a socks endpoint).
		field.String("host").Default(""),
		// port is the SOCKS proxy port (1..65535 for a socks endpoint).
		field.Int("port").Default(0),
		// socks_version is the SOCKS protocol version (4 or 5).
		field.Int("socks_version").Default(5),
		// username is the optional SOCKS auth username ("" = no auth).
		field.String("username").Default(""),
		// password is the optional SOCKS auth password. Stored PLAINTEXT at rest
		// under the single-owner homelab threat model (matches the
		// SourcePreference/TrackerConnection precedent). Marked .Sensitive() so it
		// never leaks through the generated String()/log path or JSON — the
		// handler additionally OMITS it from every response (write-only).
		field.String("password").Sensitive().Default(""),

		// --- FlareSolverr field-group (kind == "flaresolverr") ---
		// url is the FlareSolverr endpoint (absolute http(s); non-blank for a
		// flaresolverr endpoint).
		field.String("url").Default(""),
		// session is the FlareSolverr session identifier ("" = none).
		field.String("session").Default(""),
		// session_ttl is the FlareSolverr session time-to-live in minutes (≥0).
		field.Int("session_ttl").Default(0),
		// timeout is the per-request solve timeout in seconds (≥0).
		field.Int("timeout").Default(60),
		// as_response_fallback mirrors FlareSolverr's asResponseFallback flag:
		// when true the engine uses FlareSolverr only reactively (as a fallback
		// for a request the plain HTTP client sees blocked), not for every
		// request. Default TRUE — the sensible reactive-fallback default that
		// keeps an UNBOUND source's behaviour byte-for-byte unchanged and matches
		// the least-aggressive FlareSolverr posture.
		field.Bool("as_response_fallback").Default(true),

		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the NetworkEndpoint. None — an endpoint is referenced by
// SourceNetworkBinding via a plain UUID field, not an Ent edge (the
// engine-topology-entity convention; referential integrity is enforced in
// internal/network, not by an FK constraint).
func (NetworkEndpoint) Edges() []ent.Edge {
	return nil
}
