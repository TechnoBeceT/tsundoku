package category

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/technobecet/tsundoku/internal/ent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

// deletedDefaultsKey is the Settings row key under which the names of deleted
// default categories are persisted. It is bookkeeping written DIRECTLY through
// the ent client — deliberately NOT the M12 tunable-settings allowlist (which is
// reserved for owner-facing runtime knobs), so it never appears in GET/PATCH
// /api/settings and is never validated against the tunable registry.
const deletedDefaultsKey = "categories.deleted_defaults"

// loadDeletedDefaults returns the set of default-category names the owner has
// deleted. EnsureDefaults consults it so a deleted default is NOT re-seeded on
// the next startup (the reappearing-defaults bug). A missing, blank, or corrupt
// row is treated as an EMPTY set: a fresh install has never deleted anything, so
// all five defaults seed; and a corrupt value must never wedge startup — the
// worst case of treating it as empty is a benign one-time re-seed of a
// previously-deleted default (the is_default invariant still guarantees a
// fallback exists), never a crash.
func loadDeletedDefaults(ctx context.Context, client *ent.Client) (map[string]bool, error) {
	row, err := client.Settings.Query().Where(entsettings.KeyEQ(deletedDefaultsKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("category: load deleted defaults: %w", err)
	}
	if row.Value == "" {
		return map[string]bool{}, nil
	}
	var names []string
	if uErr := json.Unmarshal([]byte(row.Value), &names); uErr != nil {
		return map[string]bool{}, nil
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, nil
}

// tombstoneDefault persists that a seeded default category (by name) has been
// deleted so EnsureDefaults will not re-create it on the next startup. Every
// seeded default — INCLUDING "Other" (QCAT-296) — is tombstone-able, so a
// deliberate delete sticks across deploys; the always-present fallback is the
// is_default invariant (ensureSingleDefault), not a name-locked "Other".
//
// It is a no-op for a non-default name (only seeded defaults are ever
// auto-created, so only they need a tombstone — a user-created category simply
// stays deleted). Re-tombstoning an already-recorded name is idempotent. It uses
// the same query-then-write pattern as the settings service (no generated upsert
// helper).
func tombstoneDefault(ctx context.Context, client *ent.Client, name string) error {
	if !isDefaultName(name) {
		return nil
	}
	set, err := loadDeletedDefaults(ctx, client)
	if err != nil {
		return err
	}
	if set[name] {
		return nil
	}
	set[name] = true

	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	encoded, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("category: encode deleted defaults: %w", err)
	}
	return upsertDeletedDefaults(ctx, client, string(encoded))
}

// upsertDeletedDefaults writes the JSON-encoded tombstone set into the Settings
// table, creating the row the first time and updating it thereafter (the key
// column is unique, so a query-then-write is used — mirrors settings.upsertSettingTx).
func upsertDeletedDefaults(ctx context.Context, client *ent.Client, value string) error {
	_, err := client.Settings.Query().Where(entsettings.KeyEQ(deletedDefaultsKey)).Only(ctx)
	if ent.IsNotFound(err) {
		if cErr := client.Settings.Create().SetKey(deletedDefaultsKey).SetValue(value).Exec(ctx); cErr != nil {
			if ent.IsConstraintError(cErr) {
				// Lost the unique-key race with a concurrent write; fall through to update.
				return updateDeletedDefaults(ctx, client, value)
			}
			return fmt.Errorf("category: create deleted defaults: %w", cErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("category: query deleted defaults: %w", err)
	}
	return updateDeletedDefaults(ctx, client, value)
}

// updateDeletedDefaults overwrites the existing tombstone Settings row's value.
func updateDeletedDefaults(ctx context.Context, client *ent.Client, value string) error {
	if _, err := client.Settings.Update().
		Where(entsettings.KeyEQ(deletedDefaultsKey)).
		SetValue(value).
		Save(ctx); err != nil {
		return fmt.Errorf("category: update deleted defaults: %w", err)
	}
	return nil
}

// isDefaultName reports whether name is one of the seeded default categories.
func isDefaultName(name string) bool {
	for _, d := range defaultCategories {
		if d.name == name {
			return true
		}
	}
	return false
}
