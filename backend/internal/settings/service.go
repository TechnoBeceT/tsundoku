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

// DownloadConcurrency is the per-source download concurrency cap (DB override
// else default). The dispatcher reads it per cycle, so a change hot-reloads.
func (s *Service) DownloadConcurrency(ctx context.Context) int {
	return s.resolveInt(ctx, KeyDownloadConcurrency)
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

// ExtensionCheckInterval is the extension auto-check ticker period; 0 = disabled
// (DB override else default).
func (s *Service) ExtensionCheckInterval(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyExtensionCheckInterval)
}

// WarmupInterval is the anti-bot session warm-up ticker period; 0 = disabled
// (DB override else default).
func (s *Service) WarmupInterval(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyWarmupInterval)
}

// WarmupSlowThresholdMs is the EWMA-latency threshold (ms) above which a source
// is warmed by the WarmSlow pass (DB override else default).
func (s *Service) WarmupSlowThresholdMs(ctx context.Context) int {
	return s.resolveInt(ctx, KeyWarmupSlowThresholdMs)
}

// SearchCacheTTL is the interactive Search result-cache lifetime; 0 = disabled
// (DB override else default). Read per Get by the search cache (hot reload).
func (s *Service) SearchCacheTTL(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeySearchCacheTTL)
}

// ChapterCacheTTL is the interactive FetchChapters cache lifetime; 0 = disabled
// (DB override else default). Read per Get by the chapter cache (hot reload).
func (s *Service) ChapterCacheTTL(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyChapterCacheTTL)
}

// SourcesFailureThreshold is the consecutive-failure count above which the
// source-politeness circuit-breaker (internal/sourcegate) trips a physical
// source into cooldown (DB override else default).
func (s *Service) SourcesFailureThreshold(ctx context.Context) int {
	return s.resolveInt(ctx, KeySourcesFailureThreshold)
}

// SourcesCooldown is how long a tripped source's circuit-breaker stays open
// before it is available again (DB override else default).
func (s *Service) SourcesCooldown(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeySourcesCooldown)
}

// SourcesMinRequestDelay is the minimum politeness delay enforced between
// successive requests to the same physical source; 0 disables it (DB override
// else default).
func (s *Service) SourcesMinRequestDelay(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeySourcesMinRequestDelay)
}

// SuppressSplitParts reports whether fractional-part suppression is enabled
// (DB override else default). Read at use-time (hot reload).
func (s *Service) SuppressSplitParts(ctx context.Context) bool {
	return s.resolveBool(ctx, KeySuppressSplitParts)
}

// TrackRetryInterval is the tracker-push retry-queue drain period (DB
// override else default). Read at use-time by job.Runner.StartTrackerRetry,
// so a change hot-reloads on the next pass.
func (s *Service) TrackRetryInterval(ctx context.Context) time.Duration {
	return s.resolveDuration(ctx, KeyTrackRetryInterval)
}

// AutoUpdateTrack reports whether the reading-triggered tracker-sync push is
// currently enabled (DB override else default true). Read at use-time by
// syncsvc.Service.PushProgress, so a toggle hot-reloads on the next reader
// progress write.
func (s *Service) AutoUpdateTrack(ctx context.Context) bool {
	return s.resolveBool(ctx, KeyAutoUpdateTrack)
}

// MetadataAutoIdentify reports whether the Phase-1 metadata engine's
// background auto-identify pass is currently enabled (DB override else
// default true). Read at use-time by metadatasvc.Service.AutoIdentify, so a
// toggle hot-reloads on the very next adopt/import.
func (s *Service) MetadataAutoIdentify(ctx context.Context) bool {
	return s.resolveBool(ctx, KeyMetadataAutoIdentify)
}

// FlareSolverrEnabled reports whether Tsundoku's own use of FlareSolverr is
// currently enabled (DB override else default false). Read at use-time by the
// Kitsu Cloudflare-clearing transport's gate, so a toggle hot-reloads on the
// very next request (QCAT-238 — this is TSUNDOKU-OWNED config, never read
// from Suwayomi or an env var).
func (s *Service) FlareSolverrEnabled(ctx context.Context) bool {
	return s.resolveBool(ctx, KeyFlareSolverrEnabled)
}

// FlareSolverrURL is the FlareSolverr endpoint (DB override else default "").
// A blank value disables the Kitsu transport regardless of
// FlareSolverrEnabled — see the Kitsu transport's own gate-resolution doc
// comment.
func (s *Service) FlareSolverrURL(ctx context.Context) string {
	return s.resolve(ctx, KeyFlareSolverrURL)
}

// FlareSolverrTimeout is the per-request solve timeout in seconds (DB
// override else default 60).
func (s *Service) FlareSolverrTimeout(ctx context.Context) int {
	return s.resolveInt(ctx, KeyFlareSolverrTimeout)
}

// FlareSolverrSessionName is the FlareSolverr session identifier (DB override
// else default ""); a blank value means "no session".
func (s *Service) FlareSolverrSessionName(ctx context.Context) string {
	return s.resolve(ctx, KeyFlareSolverrSessionName)
}

// FlareSolverrSessionTTL is the session time-to-live in minutes (DB override
// else default 15) — also the TTL the Kitsu transport's local cf_clearance
// cache uses before it re-solves.
func (s *Service) FlareSolverrSessionTTL(ctx context.Context) int {
	return s.resolveInt(ctx, KeyFlareSolverrSessionTTL)
}

// FlareSolverrResponseFallback mirrors Suwayomi's own asResponseFallback flag
// (DB override else default false); stored + mirrored to Suwayomi but does
// not itself change the Kitsu transport's own behaviour — see the
// KeyFlareSolverrResponseFallback doc comment.
func (s *Service) FlareSolverrResponseFallback(ctx context.Context) bool {
	return s.resolveBool(ctx, KeyFlareSolverrResponseFallback)
}

// NotificationsEnabled reports whether the global new-chapter notifications
// toggle is currently on (DB override else default true). Read at use-time by
// the internal/notify pass (via the Toggle port), so a change hot-reloads on the
// next download cycle.
func (s *Service) NotificationsEnabled(ctx context.Context) bool {
	return s.resolveBool(ctx, KeyNotificationsEnabled)
}

// EngineSocksEnabled reports whether the engine's SOCKS proxy is currently
// enabled (DB override else default false).
func (s *Service) EngineSocksEnabled(ctx context.Context) bool {
	return s.resolveBool(ctx, KeyEngineSocksEnabled)
}

// EngineSocksHost is the SOCKS proxy hostname or IP (DB override else default "").
func (s *Service) EngineSocksHost(ctx context.Context) string {
	return s.resolve(ctx, KeyEngineSocksHost)
}

// EngineSocksPort is the SOCKS proxy port (DB override else default 1080).
func (s *Service) EngineSocksPort(ctx context.Context) int {
	return s.resolveInt(ctx, KeyEngineSocksPort)
}

// EngineSocksVersion is the SOCKS protocol version, 4 or 5 (DB override else
// default 5).
func (s *Service) EngineSocksVersion(ctx context.Context) int {
	return s.resolveInt(ctx, KeyEngineSocksVersion)
}

// ReportingRetentionDays is how many days of source-operation audit-log rows the
// daily retention purge keeps (DB override else default 30). Read at use-time by
// the purge ticker, so a change hot-reloads on the next daily sweep.
func (s *Service) ReportingRetentionDays(ctx context.Context) int {
	return s.resolveInt(ctx, KeyReportingRetentionDays)
}

// RetainedVersions is how many .apk versions per extension the apk cache keeps
// — the reversible-update history depth (DB override else default 3). Read at
// use-time by the harvest/update prune + the reinstall write-through, so a
// change hot-reloads on the next prune.
func (s *Service) RetainedVersions(ctx context.Context) int {
	return s.resolveInt(ctx, KeyRetainedVersions)
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

// ExistingKeys reports which of the given tunable keys already have an explicit
// row in the Settings table — i.e. the keys Tsundoku currently OWNS an override
// for. A key ABSENT from the returned set has no row: it is unset and still
// resolves to its injected default (the IsNotFound branch of resolve), so it is
// a "gap" a one-time seed may fill without clobbering an owner edit. It reads the
// key column only (one narrow IN query), mirroring seed-side query style; an
// empty keys slice short-circuits with no query.
func (s *Service) ExistingKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	present := make(map[string]bool, len(keys))
	if len(keys) == 0 {
		return present, nil
	}
	rows, err := s.client.Settings.Query().
		Where(entsettings.KeyIn(keys...)).
		Select(entsettings.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings.ExistingKeys: query keys: %w", err)
	}
	for _, k := range rows {
		present[k] = true
	}
	return present, nil
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

// resolveBool parses a bool-typed key's resolved canonical value, with the same
// defensive fallback as resolveDuration/resolveInt.
func (s *Service) resolveBool(ctx context.Context, key string) bool {
	b, err := strconv.ParseBool(s.resolve(ctx, key))
	if err != nil {
		return s.defaultBool(key)
	}
	return b
}

// defaultBool returns a key's injected default in its native type (used only on
// the defensive parse-failure path of resolveBool).
func (s *Service) defaultBool(key string) bool {
	b, _ := strconv.ParseBool(tunables[key].def(s.defaults))
	return b
}
