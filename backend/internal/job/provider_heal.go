package job

import (
	"context"
	"log/slog"
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

// runProviderHeal runs one provider self-heal pass. Errors are logged and
// swallowed — exactly like runUpgradeDetection — so a heal failure can never kill
// the refresh loop or abort the rest of the sweep; the next sweep retries. A
// no-op when no healer is wired, and ~free when nothing needs healing (the pass
// short-circuits after one targeting query).
func (r *Runner) runProviderHeal(ctx context.Context) {
	healer := r.providerHealer()
	if healer == nil {
		return
	}
	merged, skipped, err := healer.HealDriftedProviders(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "job.Runner: provider self-heal error", "err", err)
		return
	}
	if merged > 0 {
		slog.InfoContext(ctx, "job.Runner: provider self-heal merged drifted sources",
			"merged", merged, "skipped", skipped)
	}
}
