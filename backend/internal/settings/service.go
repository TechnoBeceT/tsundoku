package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

// Service resolves runtime tunables: DB override (when a valid Settings row
// exists) else the injected config default. It is the overlay on top of the env
// config and is the only writer of the Settings table's allowlisted keys. The
// Ent client is safe for concurrent use, so the typed accessors are safe to call
// from the ticker + dispatcher goroutines without extra locking.
type Service struct {
	client   *ent.Client
	defaults Defaults
}

// NewService builds a settings Service over the Ent client, with the
// config-resolved defaults injected by the caller (main). The service never
// reads env — defaults arrive already typed, preserving the single env boundary.
func NewService(client *ent.Client, defaults Defaults) *Service {
	return &Service{client: client, defaults: defaults}
}

// DownloadInterval is the download ticker period (DB override else default).
func (s *Service) DownloadInterval(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyDownloadInterval)
}

// RefreshInterval is the discovery-sweep ticker period (DB override else default).
func (s *Service) RefreshInterval(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyRefreshInterval)
}

// RefreshConcurrency bounds parallel provider re-fetches (DB override else default).
func (s *Service) RefreshConcurrency(ctx context.Context) int {
	return s.resolveInt(ctx, KeyRefreshConcurrency)
}

// MaxRetries is the failed-download retry budget (DB override else default).
func (s *Service) MaxRetries(ctx context.Context) int {
	return s.resolveInt(ctx, KeyMaxRetries)
}

// RetryBackoff is the base retry backoff delay (DB override else default).
func (s *Service) RetryBackoff(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyRetryBackoff)
}

// StaleGraceDays tunes the source-health stale threshold (DB override else default).
func (s *Service) StaleGraceDays(ctx context.Context) int {
	return s.resolveInt(ctx, KeyStaleGraceDays)
}

// List returns the whole allowlist in stable order with each key's current
// resolved value, default, type, and unit — the GET /api/settings payload.
func (s *Service) List(ctx context.Context) []SettingDTO {
	out := make([]SettingDTO, 0, len(tunableOrder))
	for _, key := range tunableOrder {
		t := tunables[key]
		out = append(out, SettingDTO{
			Key:     key,
			Value:   s.resolve(ctx, key),
			Default: t.def(s.defaults),
			Type:    string(t.typ),
			Unit:    t.unit,
		})
	}
	return out
}

// Set validates and upserts a single tunable. Unknown key → ErrUnknownSetting;
// an out-of-bounds or unparseable value → ErrInvalidSetting; the store therefore
// never holds an invalid value. It is the single-key form of SetMany.
func (s *Service) Set(ctx context.Context, key, value string) error {
	return s.SetMany(ctx, []KeyValue{{Key: key, Value: value}})
}

// SetMany validates EVERY update against the allowlist first (all-or-nothing:
// the first unknown key → ErrUnknownSetting, the first bad value →
// ErrInvalidSetting, both naming the offending key), then upserts all of them in
// a single transaction. No partial write ever lands when one update is invalid.
func (s *Service) SetMany(ctx context.Context, updates []KeyValue) error {
	type canonical struct{ key, value string }
	pending := make([]canonical, 0, len(updates))
	for _, u := range updates {
		t, ok := tunables[u.Key]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownSetting, u.Key)
		}
		c, err := t.validate(u.Value)
		if err != nil {
			return err // already wraps ErrInvalidSetting and names the key
		}
		pending = append(pending, canonical{key: u.Key, value: c})
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("settings.SetMany: begin tx: %w", err)
	}
	for _, p := range pending {
		if err := upsertSettingTx(ctx, tx, p.key, p.value); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings.SetMany: commit tx: %w", err)
	}
	return nil
}

// upsertSettingTx writes key=value into the Settings table, creating the row the
// first time and updating it (refreshing updated_at) thereafter. The key column
// is unique, so the find-then-write pattern is used (no upsert helper generated).
func upsertSettingTx(ctx context.Context, tx *ent.Tx, key, value string) error {
	existing, err := tx.Settings.Query().Where(entsettings.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		if cErr := tx.Settings.Create().SetKey(key).SetValue(value).Exec(ctx); cErr != nil {
			return fmt.Errorf("settings.SetMany: create %s: %w", key, cErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("settings.SetMany: query %s: %w", key, err)
	}
	if uErr := tx.Settings.UpdateOneID(existing.ID).SetValue(value).Exec(ctx); uErr != nil {
		return fmt.Errorf("settings.SetMany: update %s: %w", key, uErr)
	}
	return nil
}

// resolve returns a key's current canonical value: the validated DB override
// when a row exists, otherwise the default. A stored value that fails validation
// (which Set prevents, so this is purely defensive) or a DB read error falls back
// to the default and is logged — a tunable read must never fail a download cycle.
func (s *Service) resolve(ctx context.Context, key string) string {
	t := tunables[key]
	def := t.def(s.defaults)

	row, err := s.client.Settings.Query().Where(entsettings.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return def
	}
	if err != nil {
		slog.WarnContext(ctx, "settings: read failed, using default", "key", key, "err", err)
		return def
	}

	canonical, vErr := t.validate(row.Value)
	if vErr != nil {
		slog.WarnContext(ctx, "settings: stored value invalid, using default", "key", key, "value", row.Value, "err", vErr)
		return def
	}
	return canonical
}

// resolveDuration parses a duration-typed key's resolved canonical value. The
// canonical form is always re-parseable, so the error is structurally impossible;
// it falls back to the typed default if it ever occurs (defensive).
func (s *Service) resolveDuration(ctx context.Context, key string) time.Duration {
	d, err := time.ParseDuration(s.resolve(ctx, key))
	if err != nil {
		return s.defaultDuration(key)
	}
	return d
}

// resolveInt parses an int-typed key's resolved canonical value, with the same
// defensive fallback as resolveDuration.
func (s *Service) resolveInt(ctx context.Context, key string) int {
	n, err := strconv.Atoi(s.resolve(ctx, key))
	if err != nil {
		return s.defaultInt(key)
	}
	return n
}

// defaultDuration / defaultInt return a key's injected default in its native type
// (used only on the defensive parse-failure path of the resolvers).
func (s *Service) defaultDuration(key string) time.Duration {
	d, _ := time.ParseDuration(tunables[key].def(s.defaults))
	return d
}

func (s *Service) defaultInt(key string) int {
	n, _ := strconv.Atoi(tunables[key].def(s.defaults))
	return n
}
