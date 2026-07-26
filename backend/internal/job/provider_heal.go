package job

import (
	"context"
	"log/slog"
	"time"
)

// ProviderHealer folds already-drifted (disk-origin, live twin) provider pairs
// back into one row across the library. *library.Service satisfies it via
// HealDriftedProviders. A narrow local interface so job never imports
// internal/library, mirroring how notifier and BreakerSnapshotter are held.
//
// The implementation is pure DB + CBZ-relabel and makes ZERO live source calls;
// it deletes no CBZ. See library.HealDriftedProviders for the full safety
// argument — it is an enumerated automatic mutation path (GAP-120).
type ProviderHealer interface {
	// HealDriftedProviders merges every drifted pair it can and reports the
	// aggregate merged / skipped counts.
	HealDriftedProviders(ctx context.Context) (merged, skipped int, err error)
}

// SetProviderHealer registers the post-sweep provider self-heal. Nil-safe:
// passing nil (or never calling it) leaves the refresh sweep heal-free.
//
// It is guarded because — unlike SetNotifier / SetBreakerSnapshotter, both wired
// in main before the tickers start — the library service this healer IS gets
// constructed with the HTTP routes, which happens AFTER StartRefresh has already
// launched its goroutine. Without the lock that late write would race the sweep's
// read. A sweep that runs in the gap simply finds no healer and skips the pass.
func (r *Runner) SetProviderHealer(h ProviderHealer) {
	r.healMu.Lock()
	defer r.healMu.Unlock()
	r.healer = h
}

// providerHealer reads the registered healer under the lock (see
// SetProviderHealer), returning nil when none is wired.
func (r *Runner) providerHealer() ProviderHealer {
	r.healMu.RLock()
	defer r.healMu.RUnlock()
	return r.healer
}

// providerHealTimeout bounds ONE post-sweep self-heal pass. The heal runs
// SYNCHRONOUSLY inside runRefreshSweep, ahead of upgrade detection and the
// download Trigger, and its cost is unbounded by nature: it is a merge per drifted
// series, and a single merge rewrites every overlapping CBZ zip over NFS (~7 min
// worst case observed on prod for a 238-chapter provider). A first sweep after a
// long-drifted library came online could therefore stall detection and downloads
// for tens of minutes while the period loop skipped tick after tick.
//
// 10m matches the owner's library-wide sweep budget (handler/library's
// dedupAllTimeout), which does the same work from the same core.
//
// Cutting the pass off is a CLEAN stop, not a corrupting abort: the heal checks
// ctx between series, and within a series the DB phase is a single all-or-nothing
// tx — a cancellation lands either before it (the disk relabels are unwound and no
// row changed) or on it (the tx fails and is rolled back, then the relabels are
// unwound). A partially-completed pass is fine by design: the series it did not
// reach are still drifted, so the very next sweep resumes with them.
const providerHealTimeout = 10 * time.Minute

// runProviderHeal runs one time-bounded provider self-heal pass. Errors are logged
// and swallowed — exactly like runUpgradeDetection — so a heal failure can never
// kill the refresh loop or abort the rest of the sweep; the next sweep retries. A
// no-op when no healer is wired, and ~free when nothing needs healing (the pass
// short-circuits after one targeting query).
func (r *Runner) runProviderHeal(ctx context.Context) {
	healer := r.providerHealer()
	if healer == nil {
		return
	}
	healCtx, cancel := context.WithTimeout(ctx, providerHealTimeout)
	defer cancel()
	merged, skipped, err := healer.HealDriftedProviders(healCtx)
	if err != nil {
		slog.ErrorContext(ctx, "job.Runner: provider self-heal error", "err", err)
		return
	}
	if merged > 0 {
		slog.InfoContext(ctx, "job.Runner: provider self-heal merged drifted sources",
			"merged", merged, "skipped", skipped)
	}
}
