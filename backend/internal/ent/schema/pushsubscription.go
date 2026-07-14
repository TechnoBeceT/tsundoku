package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// PushSubscription holds the schema definition for the PushSubscription entity:
// one browser Web Push subscription, keyed by its unique push-service endpoint.
// The owner may install Tsundoku on several devices, so there is one row per
// device that has enabled notifications (mirrors Mihon/Komikku's per-device
// push registration).
//
// It is a deliberately edge-less, denormalized record (like SourceMetric /
// PendingTrackPush): a subscription is throwaway bookkeeping for the Web Push
// sender, not part of any durable library relationship. A browser that drops the
// subscription (410 Gone) simply loses its row; the owner re-enables to recreate
// it.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table.
type PushSubscription struct {
	ent.Schema
}

// Fields of the PushSubscription.
func (PushSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// endpoint is the push-service URL the browser gave us (the address the
		// VAPID-signed payload is POSTed to). UNIQUE: it is the natural identity of
		// a subscription, so re-enabling on the same device upserts this row rather
		// than piling up duplicates.
		field.String("endpoint").Unique(),
		// p256dh + auth are the subscription's public encryption keys (base64url),
		// supplied by the browser's PushManager. The sender needs both to encrypt
		// each payload for that endpoint.
		field.String("p256dh"),
		field.String("auth"),
		field.Time("created_at").Default(time.Now).Immutable(),
		// last_success_at records the most recent successful push to this
		// endpoint; nil before the first success. A diagnostics/health signal —
		// never used to gate delivery.
		field.Time("last_success_at").Optional().Nillable(),
		// failure_count counts consecutive non-2xx (non-prune) push failures. The
		// sender prunes a subscription once it crosses a hard cap, so a device that
		// silently stops accepting pushes is eventually forgotten.
		field.Int("failure_count").Default(0),
	}
}

// Edges of the PushSubscription. None — see the type doc comment for why the
// subscription is a plain denormalized record with no library relationships.
func (PushSubscription) Edges() []ent.Edge {
	return nil
}
