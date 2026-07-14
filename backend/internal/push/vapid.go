// Package push owns Tsundoku's Web Push infrastructure: the server VAPID key
// pair (auto-generated once, persisted), the per-device subscription store, and
// the VAPID-signed sender that fans a rendered notification out to every
// subscription and auto-prunes dead ones.
//
// The public key is served to the frontend (GET /api/push/vapid-key) so a
// browser can subscribe; the private key never leaves the server. Both, plus the
// watermark used by internal/notify, live in the existing Settings KV table under
// the internal.* namespace — invisible to the settings API allowlist — read and
// written by direct ent, never through settings.Service.
package push

import (
	"context"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/technobecet/tsundoku/internal/ent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

const (
	// vapidPublicKeyKey / vapidPrivateKeyKey are the Settings rows that persist
	// the server VAPID key pair (base64url). Under internal.* so the settings API
	// never exposes or overwrites them.
	vapidPublicKeyKey  = "internal.push.vapid_public"
	vapidPrivateKeyKey = "internal.push.vapid_private"
)

// EnsureVAPID returns the server's VAPID key pair, generating and persisting one
// ONLY when neither key is stored yet, and returning the same pair on every
// subsequent call (idempotent). The public key is safe to hand to clients; the
// private key signs each push and must stay server-side.
//
// A genuine read error (NOT a missing row) ABORTS with that error rather than
// regenerating: a transient DB hiccup at boot must never rotate the keypair,
// because rotating it silently invalidates EVERY existing device subscription.
// Regeneration happens only when both keys are confirmed absent.
func EnsureVAPID(ctx context.Context, client *ent.Client) (public, private string, err error) {
	pub, found, pubErr := readSetting(ctx, client, vapidPublicKeyKey)
	if pubErr != nil {
		return "", "", pubErr
	}
	priv, privFound, privErr := readSetting(ctx, client, vapidPrivateKeyKey)
	if privErr != nil {
		return "", "", privErr
	}
	if found && privFound && pub != "" && priv != "" {
		return pub, priv, nil
	}

	// GenerateVAPIDKeys returns (privateKey, publicKey) — note the order.
	newPriv, newPub, genErr := webpush.GenerateVAPIDKeys()
	if genErr != nil {
		return "", "", genErr
	}
	if wErr := writeSetting(ctx, client, vapidPublicKeyKey, newPub); wErr != nil {
		return "", "", wErr
	}
	if wErr := writeSetting(ctx, client, vapidPrivateKeyKey, newPriv); wErr != nil {
		return "", "", wErr
	}
	return newPub, newPriv, nil
}

// readSetting reads a single internal.* Settings value. found is false (no error)
// when the row is genuinely absent; a real read error is returned as-is so the
// caller can distinguish "not stored yet" from "could not read".
func readSetting(ctx context.Context, client *ent.Client, key string) (value string, found bool, err error) {
	row, qErr := client.Settings.Query().Where(entsettings.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(qErr) {
		return "", false, nil
	}
	if qErr != nil {
		return "", false, qErr
	}
	return row.Value, true, nil
}

// writeSetting upserts a single internal.* Settings value via direct ent.
func writeSetting(ctx context.Context, client *ent.Client, key, value string) error {
	existing, err := client.Settings.Query().Where(entsettings.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return client.Settings.Create().SetKey(key).SetValue(value).Exec(ctx)
	}
	if err != nil {
		return err
	}
	return client.Settings.UpdateOneID(existing.ID).SetValue(value).Exec(ctx)
}
