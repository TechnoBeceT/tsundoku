// Package sourcegate implements the source-politeness gate: a per-physical-
// source circuit-breaker (persisted) plus an in-memory politeness delay,
// consulted by every background source-access path (the download dispatcher,
// the refresh sweep, and the warm-up job) before it hits a Suwayomi source.
//
// The motivation is a live incident: an unthrottled deployment got the
// owner's home IP hard-blocked by Cloudflare on a source overnight. A
// Cloudflare block surfaces through Suwayomi's embedded WebView as a plain
// failed/empty fetch, not a clean HTTP 429 — so the gate uses a
// consecutive-failure circuit-breaker (subsumes CF blocks, rate limiting, and
// outages uniformly, with no page-parsing) rather than status-code detection.
//
// The gate is keyed by the physical-source NAME (see the callers'
// canonicalSourceKey / TrimSpace(Source.Name) helpers), NOT a numeric
// Suwayomi source id — disk-reconciled providers have no numeric id, so the
// name is the only identity computable on every gated path.
//
// Owner-interactive SEARCH is deliberately NOT gated (low-volume,
// owner-initiated; blocking a manual search would be surprising).
package sourcegate

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// maxErrorLen caps a stored last_error so a pathologically long upstream
// message can't bloat the breaker row (mirrors internal/metrics).
const maxErrorLen = 512

// Thresholds supplies the runtime-tunable politeness policy. It is resolved
// AT USE-TIME (never captured), so an owner's change via the settings API
// applies on the next call without a restart. *settings.Service and
// settings.Static both satisfy it.
type Thresholds interface {
	// SourcesFailureThreshold is the consecutive-failure count above which a
	// physical source's circuit-breaker trips into cooldown.
	SourcesFailureThreshold(ctx context.Context) int
	// SourcesCooldown is how long a tripped source's circuit-breaker stays open
	// before it is available again.
	SourcesCooldown(ctx context.Context) time.Duration
	// SourcesMinRequestDelay is the minimum gap enforced between successive
	// requests to the same physical source; 0 disables the delay.
	SourcesMinRequestDelay(ctx context.Context) time.Duration
}

// Service is the source-politeness gate. Construct one with NewService and
// share it across every background source-access path (download dispatcher,
// refresh sweep, warm-up job) — the breaker and the politeness delay must be
// keyed consistently across all three for the gate to protect a source.
//
// The breaker (SourceCircuitState) is PERSISTED — it must survive a restart, or
// a redeploy would immediately re-hammer a still-blocked source. The
// transition notifications are persisted beside their breaker mutation and
// replayed through a per-source database cursor, so concurrent processes cannot
// publish a reset ahead of the trip it follows. The politeness last-access map
// is in-memory and ephemeral — a restart merely skips one delay, which is an
// acceptable, non-safety-critical reset.
type Service struct {
	client *ent.Client
	t      Thresholds
	// events is the nil-guarded audit-log recorder. When attached (via
	// WithEventRecorder) the breaker emits a breaker_trip on the transition into
	// cooldown and a breaker_reset on the transition out of it (natural recovery
	// or an owner Reset) — transition-only, never on every failure/success. Nil
	// (the default) means no audit events are emitted, so existing call sites and
	// tests are unaffected.
	events sourceevents.Recorder

	// onTransition is the nil-guarded breaker-transition hook (see
	// WithTransitionHook / alert.go). It records each breaker STATE TRANSITION — a
	// trip and a clear — so an owner (the job.Runner) can push an immediate
	// sources.summary alert. sourcegate stays SSE-free: this is an opaque callback,
	// never the hub. Nil (the default) fires nothing.
	onTransition func(BreakerTransition)

	mu         sync.Mutex
	lastAccess map[string]time.Time
}

// NewService builds a sourcegate Service over the Ent client, with the
// runtime-tunable thresholds resolved at use-time from t.
func NewService(client *ent.Client, t Thresholds) *Service {
	return &Service{
		client:     client,
		t:          t,
		lastAccess: make(map[string]time.Time),
	}
}

// WithEventRecorder attaches the source-operation audit-log recorder so the
// breaker emits breaker_trip / breaker_reset events on its state transitions. It
// returns the receiver for chaining off NewService. A nil recorder emits nothing
// (the default). The recorder is best-effort, so a logging failure never affects
// the breaker's own bookkeeping.
func (s *Service) WithEventRecorder(r sourceevents.Recorder) *Service {
	s.events = r
	return s
}

// logBreakerEvent records a breaker transition (best-effort, nil-guarded). It
// carries only the source_key (which IS the trimmed source name — the breaker
// has no numeric id or language), so both source_key and source_name are the key.
func (s *Service) logBreakerEvent(
	ctx context.Context,
	key string,
	eventType sourceevents.EventType,
	status sourceevents.Status,
	cause error,
	errorCategory string,
) {
	if s.events == nil {
		return
	}
	s.events.Log(ctx, sourceevents.Event{
		SourceKey:     key,
		SourceName:    key,
		Type:          eventType,
		Status:        status,
		Err:           cause,
		ErrorCategory: errorCategory,
	})
}

// IsAvailable reports whether key's circuit-breaker currently permits access:
// true when no breaker row exists, cooldown_until is unset, or cooldown_until
// is at or before now. FAILS OPEN (returns true) on a read error — the gate
// must never wedge a download because its own bookkeeping table is
// unreadable; the error is logged for operator visibility.
func (s *Service) IsAvailable(ctx context.Context, key string, now time.Time) bool {
	row, err := s.client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return true
	}
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: IsAvailable read failed, failing open",
			"source_key", key, "err", err)
		return true
	}
	if row.CooldownUntil == nil {
		return true
	}
	return !row.CooldownUntil.After(now)
}

// BreakerState is a read-only snapshot of one source's circuit-breaker row,
// returned by Snapshot for the read models that surface breaker state alongside
// their own data (the source-metrics screen). It is a plain value so callers
// outside sourcegate never touch the Ent row directly.
type BreakerState struct {
	// SourceKey is the physical-source identity the breaker is keyed by (the
	// trimmed display name — see the package doc comment).
	SourceKey string
	// ConsecutiveFailures is how many gated fetches have failed in a row.
	ConsecutiveFailures int
	// CooldownUntil is when the tripped breaker reopens; nil when not tripped.
	CooldownUntil *time.Time
	// FailingSince marks the start of the current failure streak; nil when the
	// source is not currently failing. Answers "erroring since when" without an
	// event-log scan (see the schema field's doc comment).
	FailingSince *time.Time
	// LastError is the most recent gated-fetch failure reason ("" when none).
	LastError string
	// UpdatedAt is when the breaker row was last written.
	UpdatedAt time.Time
}

// IsCoolingDown reports whether the breaker is currently tripped at now: a
// cooldown is set and still in the future. It is the read-model mirror of
// IsAvailable (a source is cooling down exactly when it is not available because
// of a live cooldown), kept here so the rule lives in one place.
func (b BreakerState) IsCoolingDown(now time.Time) bool {
	return b.CooldownUntil != nil && b.CooldownUntil.After(now)
}

// Snapshot returns every source's current breaker state keyed by source_key, in
// ONE query — the batch read a read model joins against so it never issues a
// per-source breaker lookup (no N+1). An empty map is returned when no breaker
// rows exist. Unlike the best-effort writers this RETURNS its read error: the
// caller (a read endpoint) decides whether a missing join is fatal.
func (s *Service) Snapshot(ctx context.Context) (map[string]BreakerState, error) {
	rows, err := s.client.SourceCircuitState.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sourcegate.Snapshot: %w", err)
	}
	out := make(map[string]BreakerState, len(rows))
	for _, r := range rows {
		out[r.SourceKey] = BreakerState{
			SourceKey:           r.SourceKey,
			ConsecutiveFailures: r.ConsecutiveFailures,
			CooldownUntil:       r.CooldownUntil,
			FailingSince:        r.FailingSince,
			LastError:           r.LastError,
			UpdatedAt:           r.UpdatedAt,
		}
	}
	return out, nil
}

// Reset clears key's tripped circuit-breaker: it DELETES the breaker row, so the
// source is immediately available again (consecutive_failures back to 0, no
// cooldown, no residual last_error). This is the owner "reset source" action — a
// deliberate override of the anti-ban cooldown for one source. It is:
//   - idempotent: deleting zero rows is not an error, so it is a safe no-op when
//     the source has no breaker row;
//   - scoped to exactly key: no other source's breaker and no global gating
//     behaviour is affected (gating stays fully in force for every other source);
//   - error-RETURNING (unlike the best-effort recorders) so the handler can
//     surface a failure to the owner (§16).
func (s *Service) Reset(ctx context.Context, key string) error {
	tx, _, err := s.lockCircuitState(ctx, key)
	if err != nil {
		return fmt.Errorf("sourcegate.Reset: lock breaker %q: %w", key, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.SourceCircuitState.Delete().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Exec(ctx); err != nil {
		return fmt.Errorf("sourcegate.Reset: delete breaker %q: %w", key, err)
	}
	queued, err := s.enqueueTransition(ctx, tx, BreakerTransition{
		SourceKey: key,
		EventType: sourceevents.EventBreakerReset,
	}, sourceevents.StatusSuccess, nil)
	if err != nil {
		return fmt.Errorf("sourcegate.Reset: enqueue reset for %q: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sourcegate.Reset: commit reset for %q: %w", key, err)
	}
	committed = true
	if queued {
		s.publishSourceTransitions(ctx, key)
	}
	return nil
}

// Clear DELETES key's breaker row and returns how many rows were removed (0 or
// 1). It is the source-PURGE counterpart to Reset: Reset is the owner's "un-trip
// this still-present source" override (and so logs a breaker_reset transition +
// fires the sources.summary alert), whereas Clear removes a source that is being
// DELETED entirely — so it emits NO transition event/alert (a breaker_reset for a
// source that no longer exists would be a misleading audit entry). Error-RETURNING
// (unlike the best-effort recorders) so the purge reports exactly what it removed
// (§16). Idempotent — deleting zero rows is not an error.
func (s *Service) Clear(ctx context.Context, key string) (int, error) {
	n, err := s.client.SourceCircuitState.Delete().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("sourcegate.Clear: delete breaker %q: %w", key, err)
	}
	return n, nil
}

// RecordSuccess resets key's consecutive-failure counter and clears any
// cooldown, upserting the row if it does not yet exist. The row stays locked
// through the state change and durable notification enqueue, so a simultaneous
// failure is ordered before or after this complete reset rather than
// interleaving its fields. Publication is separately serialized by the durable
// per-source cursor. This is database coordination, not a process-local lock:
// separate binaries share both rows. Best-effort: a DB failure is logged and
// swallowed.
func (s *Service) RecordSuccess(ctx context.Context, key string) {
	tx, row, err := s.lockCircuitState(ctx, key)
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: RecordSuccess failed (best-effort, skipping)",
			"source_key", key, "err", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// The verdict comes from the row protected by this transaction's write lock,
	// so it is the same serial transition that the update below commits.
	wasTripped := row.CooldownUntil != nil
	err = tx.SourceCircuitState.UpdateOne(row).
		SetConsecutiveFailures(0).
		SetLastError("").
		ClearCooldownUntil().
		ClearFailingSince().
		Exec(ctx)
	queued := false
	if err == nil && wasTripped {
		queued, err = s.enqueueTransition(ctx, tx, BreakerTransition{
			SourceKey: key,
			EventType: sourceevents.EventBreakerReset,
		}, sourceevents.StatusSuccess, nil)
	}
	if err == nil {
		err = tx.Commit()
		committed = err == nil
	}
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: RecordSuccess failed (best-effort, skipping)",
			"source_key", key, "err", err)
		return
	}
	if queued {
		s.publishSourceTransitions(ctx, key)
	}
}

// RecordFailure bumps key's consecutive-failure counter and stores cause as
// last_error, upserting the row if it does not yet exist. Once the counter
// reaches the runtime-tunable failure threshold, it trips the breaker:
// cooldown_until = now + the runtime-tunable cooldown. A PostgreSQL row lock
// makes the increment, threshold decision, cooldown, and durable notification
// enqueue one database transition across processes. The publication cursor then
// preserves that transition order across processes. Best-effort: a DB failure is
// logged and swallowed.
func (s *Service) RecordFailure(ctx context.Context, key string, cause error, now time.Time) {
	threshold := s.t.SourcesFailureThreshold(ctx)
	msg := truncateError(cause)
	tx, row, err := s.lockCircuitState(ctx, key)
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: RecordFailure failed (best-effort, skipping)",
			"source_key", key, "err", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	newFailures := row.ConsecutiveFailures + 1
	// CooldownUntil != nil remains the true pre-failure tripped state even when
	// expired: only RecordSuccess clears it, and an expired cooldown can still
	// produce the natural-recovery reset notification on that success.
	wasTripped := row.CooldownUntil != nil
	u := tx.SourceCircuitState.UpdateOne(row).
		SetConsecutiveFailures(newFailures).
		SetLastError(msg)
	if row.ConsecutiveFailures == 0 {
		u = u.SetFailingSince(now)
	}
	tripped := false
	var cooldownUntil *time.Time
	if newFailures >= threshold {
		until := now.Add(s.t.SourcesCooldown(ctx))
		cooldownUntil = &until
		u = u.SetCooldownUntil(until)
		tripped = !wasTripped
	}
	err = u.Exec(ctx)
	queued := false
	if err == nil && tripped {
		queued, err = s.enqueueTripTransition(ctx, tx, key, row, newFailures, cooldownUntil, msg, cause, now)
	}
	if err == nil {
		err = tx.Commit()
		committed = err == nil
	}
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: RecordFailure failed (best-effort, skipping)",
			"source_key", key, "err", err)
		return
	}
	if queued {
		s.publishSourceTransitions(ctx, key)
	}
}

func (s *Service) enqueueTripTransition(
	ctx context.Context,
	tx *ent.Tx,
	key string,
	previous *ent.SourceCircuitState,
	newFailures int,
	cooldownUntil *time.Time,
	message string,
	cause error,
	now time.Time,
) (bool, error) {
	failingSince := previous.FailingSince
	if previous.ConsecutiveFailures == 0 {
		started := now
		failingSince = &started
	}
	return s.enqueueTransition(ctx, tx, BreakerTransition{
		SourceKey: key,
		EventType: sourceevents.EventBreakerTrip,
		State: &BreakerState{
			SourceKey:           key,
			ConsecutiveFailures: newFailures,
			CooldownUntil:       cooldownUntil,
			FailingSince:        failingSince,
			LastError:           message,
			UpdatedAt:           time.Now().UTC(),
		},
	}, sourceevents.StatusFailed, cause)
}

// lockCircuitState ensures one row exists, takes its PostgreSQL write lock, and
// returns its current state while the transaction remains open. The initial
// conflict-safe insert handles concurrent first failures; the following update
// is deliberately a no-semantic-change lock write (updated_at is refreshed by
// every successful record operation anyway). Callers must commit or roll back
// the returned transaction.
func (s *Service) lockCircuitState(ctx context.Context, key string) (*ent.Tx, *ent.SourceCircuitState, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, nil, err
	}
	abort := func(cause error) (*ent.Tx, *ent.SourceCircuitState, error) {
		_ = tx.Rollback()
		return nil, nil, cause
	}
	if err := tx.SourceCircuitState.Create().
		SetSourceKey(key).
		OnConflictColumns(entsourcecircuitstate.FieldSourceKey).
		Update(func(u *ent.SourceCircuitStateUpsert) {
			u.SetSourceKey(key)
		}).
		Exec(ctx); err != nil {
		return abort(err)
	}
	locked, err := tx.SourceCircuitState.Update().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		SetUpdatedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return abort(err)
	}
	if locked != 1 {
		return abort(fmt.Errorf("sourcegate: lock breaker %q updated %d rows", key, locked))
	}
	row, err := tx.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(ctx)
	if err != nil {
		return abort(err)
	}
	return tx, row, nil
}

// Wait blocks, if necessary, until the runtime-tunable politeness delay has
// elapsed since key's last RESERVED slot, then reserves the slot it just
// waited for as the new last access. A delay of 0 (disabled) is a no-op.
//
// The reservation (leaky-bucket) design — computing and storing this call's
// slot under the lock BEFORE sleeping, rather than sleeping first and stamping
// "now" after — is what makes concurrent callers for the SAME key queue up
// strictly ≥delay apart from EACH OTHER (not just from whatever the map held
// when each one happened to read it): the second of two simultaneous callers
// sees the first's reserved slot and reserves slot+delay for itself, and so on.
// Callers for different keys never block each other (they touch different map
// entries, held only briefly under the mutex).
//
// Respects ctx cancellation: a cancelled context returns immediately without
// finishing the wait (the slot is still reserved, so a later call for the same
// key is not granted an unearned early slot).
func (s *Service) Wait(ctx context.Context, key string) {
	delay := s.t.SourcesMinRequestDelay(ctx)
	if delay <= 0 {
		return
	}

	now := time.Now()
	s.mu.Lock()
	slot := now
	if last, ok := s.lastAccess[key]; ok {
		if next := last.Add(delay); next.After(slot) {
			slot = next
		}
	}
	s.lastAccess[key] = slot
	s.mu.Unlock()

	wait := time.Until(slot)
	if wait <= 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// truncateError renders an error, capping the stored message at maxErrorLen.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > maxErrorLen {
		return msg[:maxErrorLen]
	}
	return msg
}
