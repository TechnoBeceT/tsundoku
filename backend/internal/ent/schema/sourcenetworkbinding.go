package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceNetworkBinding holds the schema definition for the SourceNetworkBinding
// entity: the per-source network-routing assignment (per-source network
// routing, QCAT-283). At most ONE binding exists per source (source_id is
// unique) — its ABSENCE means the source uses today's global default
// (zero-disruption), so a binding row only exists once the owner assigns a
// non-default route.
//
// A binding names up to two NetworkEndpoint rows by their plain UUID:
//
//   - socks_endpoint_id — the "socks" endpoint the source egresses through, or
//     null for direct/global SOCKS (no per-source SOCKS override).
//   - flare_endpoint_id — the "flaresolverr" endpoint the source's challenge
//     solving is routed to; REQUIRED iff flare_mode == "endpoint", forbidden
//     otherwise (enforced in internal/network).
//
// flare_mode selects the FlareSolverr routing scope for the source:
// "none" (never solve), "global" (today's shared FlareSolverr — the default),
// or "endpoint" (route to the flare_endpoint_id endpoint).
//
// The two endpoint ids are NOT modeled as Ent edges (the engine-topology-entity
// convention, mirroring SourcePreference/DisabledSource) — referential
// integrity (the referenced endpoint exists and matches the expected kind) is
// enforced in internal/network, not by an FK constraint.
//
// Every field is additive/defaulted, so adding this entity is a zero-data
// migration: Ent auto-migrate creates the empty table and bindings are created
// lazily the first time the owner assigns a source a non-default route.
type SourceNetworkBinding struct {
	ent.Schema
}

// Fields of the SourceNetworkBinding.
func (SourceNetworkBinding) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_id is the engine-host stable numeric source id and the stable
		// identity of the row. Unique — a source is bound at most once (absence =
		// unbound = global default).
		field.Int64("source_id").Unique(),
		// socks_endpoint_id points at the "socks" NetworkEndpoint this source
		// egresses through; null = direct/global SOCKS (no per-source override).
		field.UUID("socks_endpoint_id", uuid.UUID{}).Optional().Nillable(),
		// flare_mode is the FlareSolverr routing scope: "none" | "global" |
		// "endpoint". Defaults to "global" — the same shared FlareSolverr today's
		// sources use.
		field.String("flare_mode").Default("global"),
		// flare_endpoint_id points at the "flaresolverr" NetworkEndpoint this
		// source's challenge solving is routed to; required iff flare_mode ==
		// "endpoint", null otherwise.
		field.UUID("flare_endpoint_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceNetworkBinding. None — the binding references
// NetworkEndpoint rows via plain UUID fields (see the type doc comment), not Ent
// edges, and has no link to any Tsundoku series row.
func (SourceNetworkBinding) Edges() []ent.Edge {
	return nil
}
