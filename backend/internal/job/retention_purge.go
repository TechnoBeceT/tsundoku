package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// retentionPurgeInterval is the FIXED cadence of the audit-log retention sweep
// (daily). The retention WINDOW (how many days of rows to keep) is the separate,
// runtime-tunable reporting.retention_days — read at use-time via the
// retentionDays accessor, so a window change hot-reloads on the next daily sweep
// without changing this cadence.
const retentionPurgeInterval = 24 * time.Hour

// StartRetentionPurge launches a background goroutine that deletes
// source-operation audit-log rows (SourceEvent) older than the retention window.
// The first purge runs at the TOP of the loop (immediately at boot, so a
// long-lived deployment does not wait a full day for the first sweep), then the
// loop waits retentionPurgeInterval between passes. The window (reporting.
// retention_days) is re-read every pass, so a runtime change takes effect on the
// next daily sweep. A purge error is logged and the loop continues (the next
// sweep retries). Returns immediately.
func (r *Runner) StartRetentionPurge(ctx context.Context, svc *sourceevents.Service, retentionDays func(context.Context) int) {
	go func() {
		for {
			r.runRetentionPurge(ctx, svc, retentionDays)
			timer := time.NewTimer(retentionPurgeInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "job.Runner: retention-purge loop stopped (context cancelled)")
				return
			case <-timer.C:
			}
		}
	}()
}

// runRetentionPurge deletes audit-log rows older than now - retention_days and
// logs how many it removed (or the error, continuing to the next sweep). The
// window is clamped to at least 1 day (a 0/negative value would purge everything
// — the settings bounds already enforce >= 1, this is belt-and-braces).
func (r *Runner) runRetentionPurge(ctx context.Context, svc *sourceevents.Service, retentionDays func(context.Context) int) {
	days := retentionDays(ctx)
	if days < 1 {
		days = 1
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	removed, err := svc.PurgeOld(ctx, cutoff)
	if err != nil {
		slog.WarnContext(ctx, "job.Runner: audit-log retention purge failed", "err", err)
		return
	}
	if removed > 0 {
		slog.InfoContext(ctx, "job.Runner: audit-log retention purge complete", "removed", removed, "retention_days", days)
	}
}
