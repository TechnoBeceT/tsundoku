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
	"log/slog"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
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
// politeness last-access map is in-memory and ephemeral — a restart merely
// skips one delay, which is an acceptable, non-safety-critical reset.
type Service struct {
	client *ent.Client
	t      Thresholds

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

// RecordSuccess resets key's consecutive-failure counter and clears any
// cooldown, upserting the row if it does not yet exist. Best-effort: a DB
// failure is logged and swallowed — breaker bookkeeping must never break or
// slow the caller (mirrors internal/metrics.Recorder).
func (s *Service) RecordSuccess(ctx context.Context, key string) {
	row, err := s.client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(ctx)
	switch {
	case ent.IsNotFound(err):
		err = s.client.SourceCircuitState.Create().
			SetSourceKey(key).
			SetConsecutiveFailures(0).
			SetLastError("").
			Exec(ctx)
	case err != nil:
		// fall through to the warning below with the query error.
	default:
		err = s.client.SourceCircuitState.UpdateOne(row).
			SetConsecutiveFailures(0).
			SetLastError("").
			ClearCooldownUntil().
			Exec(ctx)
	}
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: RecordSuccess failed (best-effort, skipping)",
			"source_key", key, "err", err)
	}
}

// RecordFailure bumps key's consecutive-failure counter and stores cause as
// last_error, upserting the row if it does not yet exist. Once the counter
// reaches the runtime-tunable failure threshold, it trips the breaker:
// cooldown_until = now + the runtime-tunable cooldown. Best-effort: a DB
// failure is logged and swallowed.
func (s *Service) RecordFailure(ctx context.Context, key string, cause error, now time.Time) {
	msg := truncateError(cause)
	threshold := s.t.SourcesFailureThreshold(ctx)

	row, err := s.client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(ctx)
	switch {
	case ent.IsNotFound(err):
		newFailures := 1
		c := s.client.SourceCircuitState.Create().
			SetSourceKey(key).
			SetConsecutiveFailures(newFailures).
			SetLastError(msg)
		if newFailures >= threshold {
			c = c.SetCooldownUntil(now.Add(s.t.SourcesCooldown(ctx)))
		}
		err = c.Exec(ctx)
	case err != nil:
		// fall through to the warning below with the query error.
	default:
		newFailures := row.ConsecutiveFailures + 1
		u := s.client.SourceCircuitState.UpdateOne(row).
			SetConsecutiveFailures(newFailures).
			SetLastError(msg)
		if newFailures >= threshold {
			u = u.SetCooldownUntil(now.Add(s.t.SourcesCooldown(ctx)))
		}
		err = u.Exec(ctx)
	}
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: RecordFailure failed (best-effort, skipping)",
			"source_key", key, "err", err)
	}
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
