// Package warmup keeps anti-bot (Cloudflare-protected) Suwayomi sources warm.
//
// Such sources are slow ONLY on a cold session: the first request forces a
// headless-browser challenge solve (10-60s) whose clearance is then cached with a
// TTL. Periodically hitting a slow source with a cheap Browse call refreshes that
// clearance, so interactive search stays fast. The warm-up pass is driven by
// job.Runner.StartWarmup; it records each warm as a metrics sample and stamps
// last_warmed_at.
package warmup

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// sessionWarmTTL is how long a warmed anti-bot session is trusted before WarmSlow
// re-warms it. Set below the typical FlareSolverr clearance TTL (~15m, matching the
// frontend's warm-badge cutoff) so a scheduled pass refreshes a source's clearance
// before it lapses. Deliberately a constant, not a settings tunable, to avoid the
// SlowThreshold-interface blast radius for a single Minor; promote to a settings key
// later if runtime tuning is wanted.
const sessionWarmTTL = 12 * time.Minute

// SlowThreshold supplies the EWMA-latency threshold (ms) above which a source is
// considered slow. *settings.Service and settings.Static both satisfy it; it is
// read at use-time so an owner's change applies on the next pass (hot reload).
type SlowThreshold interface {
	WarmupSlowThresholdMs(ctx context.Context) int
}

// Service runs anti-bot session warm-up passes over the Suwayomi source set. It
// holds the Suwayomi client (whose BaseURL() targets the active embedded or
// external instance), the metrics store (source of the slow/never-measured
// signal and sink for each warm's sample), and the slow-threshold provider.
type Service struct {
	client  suwayomi.Client
	metrics *metrics.Service
	slow    SlowThreshold
	gate    *sourcegate.Service
}

// NewService constructs a warm-up Service. gate is the source-politeness
// circuit-breaker + delay (internal/sourcegate), consulted before each source
// is warmed — see warmOne. gate may be nil (no gate configured): every
// gate-consulting call site treats a nil gate as "always available, no
// delay" (today's pre-politeness behaviour), so passing nil is a safe
// default for callers that do not need the gate.
func NewService(client suwayomi.Client, m *metrics.Service, slow SlowThreshold, gate *sourcegate.Service) *Service {
	return &Service{client: client, metrics: m, slow: slow, gate: gate}
}

// WarmAll warms EVERY enabled online source (the seed pass). It uses the SAME
// source set Search fans out to — all sources minus Suwayomi's built-in Local
// source and any the owner disabled (enabledOnlineSources, sources.go).
// Returns the number of sources warmed successfully.
func (s *Service) WarmAll(ctx context.Context) (int, error) {
	sources, err := enabledOnlineSources(ctx, s.client)
	if err != nil {
		return 0, err
	}
	return s.warmSources(ctx, sources), nil
}

// WarmSlow warms only the sources that need it, on EITHER of two additive arms:
//   - SLOW: never measured (absent from the metrics snapshot) OR whose rolling EWMA
//     latency exceeds the current slow threshold (metrics.IsSlow).
//   - STALE-WARM: warmed too long ago (or never), so its cached anti-bot clearance
//     may have lapsed (metrics.IsStaleWarm with sessionWarmTTL) — independent of
//     latency, since a fast source still goes cold once its clearance TTL elapses.
//
// It reads the eligible source set and the threshold at use-time, and snapshots now
// once so every source is judged against the same clock. Returns the number of
// sources warmed successfully.
func (s *Service) WarmSlow(ctx context.Context) (int, error) {
	sources, err := enabledOnlineSources(ctx, s.client)
	if err != nil {
		return 0, err
	}
	snap, err := s.metrics.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	threshold := s.slow.WarmupSlowThresholdMs(ctx)
	now := time.Now()

	slow := make([]suwayomi.Source, 0, len(sources))
	for _, src := range sources {
		m := snap[src.ID]
		if metrics.IsSlow(m, threshold) || metrics.IsStaleWarm(m, sessionWarmTTL, now) {
			slow = append(slow, src)
		}
	}
	return s.warmSources(ctx, slow), nil
}

// warmSources warms each source SERIALLY (they bottleneck at Suwayomi's single
// embedded WebView anyway, and serial access is less bot-like). A per-source
// failure is logged and skipped so one bad source never aborts the pass; only
// successful warms are counted.
func (s *Service) warmSources(ctx context.Context, sources []suwayomi.Source) int {
	warmed := 0
	for _, src := range sources {
		if err := s.warmOne(ctx, src); err != nil {
			slog.WarnContext(ctx, "warmup: source warm failed (skipping)",
				"source", src.ID, "source_name", src.Name, "err", err)
			continue
		}
		warmed++
	}
	return warmed
}

// warmOne warms a single source with the cheapest call that refreshes its anti-bot
// session — Browse(POPULAR, page 1). It first checks the source-politeness gate
// (a cooled-down source is skipped entirely, returning a descriptive error so
// warmSources logs + skips it without a wasted Browse call — repeatedly warming
// a source that is already tripped would only prolong the block), then enforces
// the politeness delay, then records the timing + outcome as a metrics sample
// regardless of success (a slow/failed warm is itself signal), then stamps
// last_warmed_at ONLY on success (a failed warm did not actually warm the
// session). Returns the Browse (or gate) error so warmSources can skip + log it.
func (s *Service) warmOne(ctx context.Context, src suwayomi.Source) error {
	key := sourceKey(src)
	if !s.gateAvailable(ctx, key, time.Now()) {
		return errGateCooldown
	}
	s.gateWait(ctx, key)

	start := time.Now()
	_, err := s.client.Browse(ctx, src.ID, suwayomi.BrowsePopular, 1)
	now := time.Now()
	s.metrics.Record(ctx, src.ID, src.Name, now.Sub(start), err)
	if err != nil {
		s.gateRecordFailure(ctx, key, err, now)
		return err
	}
	s.gateRecordSuccess(ctx, key)
	if serr := s.metrics.SetWarmed(ctx, src.ID, src.Name, now); serr != nil {
		// Best-effort: the source WAS warmed even if the stamp write failed.
		slog.WarnContext(ctx, "warmup: set last_warmed_at failed",
			"source", src.ID, "err", serr)
	}
	return nil
}

// errGateCooldown is returned by warmOne when the source-politeness gate has
// the source cooled down, so warmSources logs + skips it as it would any other
// warm failure.
var errGateCooldown = errors.New("warmup: source cooled down by politeness gate")

// sourceKey returns the physical-source identity used to key the
// source-politeness gate for a Suwayomi Source: its trimmed display name. It
// mirrors download.canonicalSourceKey / refresh.sourceKey — kept as a small
// local copy rather than a cross-package import.
func sourceKey(src suwayomi.Source) string {
	return strings.TrimSpace(src.Name)
}

// gateAvailable reports whether sourceKey's circuit-breaker currently permits
// access. A nil gate (no gate configured) is always available.
func (s *Service) gateAvailable(ctx context.Context, sourceKey string, now time.Time) bool {
	if s.gate == nil {
		return true
	}
	return s.gate.IsAvailable(ctx, sourceKey, now)
}

// gateWait enforces the politeness delay for sourceKey before a Browse call. A
// nil gate is a no-op.
func (s *Service) gateWait(ctx context.Context, sourceKey string) {
	if s.gate == nil {
		return
	}
	s.gate.Wait(ctx, sourceKey)
}

// gateRecordSuccess reports a successful warm from sourceKey to the breaker. A
// nil gate is a no-op.
func (s *Service) gateRecordSuccess(ctx context.Context, sourceKey string) {
	if s.gate == nil {
		return
	}
	s.gate.RecordSuccess(ctx, sourceKey)
}

// gateRecordFailure reports a failed warm from sourceKey to the breaker. A nil
// gate is a no-op.
func (s *Service) gateRecordFailure(ctx context.Context, sourceKey string, cause error, now time.Time) {
	if s.gate == nil {
		return
	}
	s.gate.RecordFailure(ctx, sourceKey, cause, now)
}
