package settings

import (
	"context"
	"time"
)

// Static is a fixed-value tunable provider: every accessor returns the value it
// was constructed with, ignoring the DB. It is for wiring or tests that do not
// need runtime tuning — it satisfies the same accessor surface as *Service
// (download.RetrySettings, job.Intervals, refresh.Concurrency, and the
// series stale-grace resolver) so either can be threaded into a consumer.
type Static struct {
	Download    time.Duration
	Refresh     time.Duration
	Concurrency int
	Retries     int
	Backoff     time.Duration
	StaleGrace  int
}

// DownloadInterval returns the fixed download ticker period.
func (s Static) DownloadInterval(context.Context) time.Duration { return s.Download }

// RefreshInterval returns the fixed discovery-sweep period.
func (s Static) RefreshInterval(context.Context) time.Duration { return s.Refresh }

// RefreshConcurrency returns the fixed parallel-refetch bound.
func (s Static) RefreshConcurrency(context.Context) int { return s.Concurrency }

// MaxRetries returns the fixed failed-download retry budget.
func (s Static) MaxRetries(context.Context) int { return s.Retries }

// RetryBackoff returns the fixed base retry backoff delay.
func (s Static) RetryBackoff(context.Context) time.Duration { return s.Backoff }

// StaleGraceDays returns the fixed source-health stale threshold.
func (s Static) StaleGraceDays(context.Context) int { return s.StaleGrace }
