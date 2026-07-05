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
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

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
}

// NewService constructs a warm-up Service.
func NewService(client suwayomi.Client, m *metrics.Service, slow SlowThreshold) *Service {
	return &Service{client: client, metrics: m, slow: slow}
}

// WarmAll warms EVERY enabled online source (the seed pass). It uses the SAME
// source set Search fans out to — all sources minus Suwayomi's built-in Local
// source and any the owner disabled (imports.EnabledOnlineSources, §2 DRY).
// Returns the number of sources warmed successfully.
func (s *Service) WarmAll(ctx context.Context) (int, error) {
	sources, err := imports.EnabledOnlineSources(ctx, s.client)
	if err != nil {
		return 0, err
	}
	return s.warmSources(ctx, sources), nil
}

// WarmSlow warms only the sources that need it: those never measured (absent from
// the metrics snapshot) OR whose rolling EWMA latency exceeds the current slow
// threshold (metrics.IsSlow). It reads both the eligible source set and the
// threshold at use-time. Returns the number of sources warmed successfully.
func (s *Service) WarmSlow(ctx context.Context) (int, error) {
	sources, err := imports.EnabledOnlineSources(ctx, s.client)
	if err != nil {
		return 0, err
	}
	snap, err := s.metrics.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	threshold := s.slow.WarmupSlowThresholdMs(ctx)

	slow := make([]suwayomi.Source, 0, len(sources))
	for _, src := range sources {
		if metrics.IsSlow(snap[src.ID], threshold) {
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
// session — Browse(POPULAR, page 1). It records the timing + outcome as a metrics
// sample regardless of success (a slow/failed warm is itself signal), then stamps
// last_warmed_at ONLY on success (a failed warm did not actually warm the
// session). Returns the Browse error so warmSources can skip + log it.
func (s *Service) warmOne(ctx context.Context, src suwayomi.Source) error {
	start := time.Now()
	_, err := s.client.Browse(ctx, src.ID, suwayomi.BrowsePopular, 1)
	s.metrics.Record(ctx, src.ID, src.Name, time.Since(start), err)
	if err != nil {
		return err
	}
	if serr := s.metrics.SetWarmed(ctx, src.ID, src.Name, time.Now()); serr != nil {
		// Best-effort: the source WAS warmed even if the stamp write failed.
		slog.WarnContext(ctx, "warmup: set last_warmed_at failed",
			"source", src.ID, "err", serr)
	}
	return nil
}
