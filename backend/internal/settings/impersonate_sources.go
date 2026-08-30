package settings

import (
	"context"
	"fmt"
	"slices"

	"entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

// UpdateImpersonateSourceTx changes one source's image-proxy membership inside
// the caller's transaction. The canonical allowlist row is created on first
// use and locked before it is read, so concurrent source-scoped changes compose
// instead of replacing one another. The returned set is de-duplicated and in
// ascending order.
func (s *Service) UpdateImpersonateSourceTx(ctx context.Context, tx *ent.Tx, sourceID int64, enabled bool) ([]int64, error) {
	t := tunables[KeyImpersonateSources]
	defaultValue, err := t.validate(t.def(s.defaults))
	if err != nil {
		return nil, fmt.Errorf("settings.UpdateImpersonateSourceTx: validate default: %w", err)
	}
	if err := tx.Settings.Create().
		SetKey(KeyImpersonateSources).
		SetValue(defaultValue).
		OnConflictColumns(entsettings.FieldKey).
		Ignore().
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("settings.UpdateImpersonateSourceTx: ensure canonical row: %w", err)
	}

	row, err := tx.Settings.Query().Where(predicate.Settings(func(selector *sql.Selector) {
		selector.Where(sql.EQ(selector.C(entsettings.FieldKey), KeyImpersonateSources))
		selector.ForUpdate()
	})).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings.UpdateImpersonateSourceTx: lock canonical row: %w", err)
	}
	ids, err := parseSourceIDSet(row.Value)
	if err != nil {
		return nil, fmt.Errorf("settings.UpdateImpersonateSourceTx: parse stored membership: %w", err)
	}

	index, found := slices.BinarySearch(ids, sourceID)
	switch {
	case enabled && !found:
		ids = append(ids, 0)
		copy(ids[index+1:], ids[index:])
		ids[index] = sourceID
	case !enabled && found:
		ids = slices.Delete(ids, index, index+1)
	}
	if err := tx.Settings.UpdateOneID(row.ID).SetValue(formatSourceIDSet(ids)).Exec(ctx); err != nil {
		return nil, fmt.Errorf("settings.UpdateImpersonateSourceTx: store membership: %w", err)
	}
	return ids, nil
}
