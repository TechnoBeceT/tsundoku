package enginetopo

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// SeedDeps bundles the collaborators the one-shot topology seed needs. It is a
// plain struct so main.go can wire the shared, construct-once instances (the
// same suwayomi client, ent client, apkcache store, and settings service the
// rest of the app uses) into RunSeed's single background call without a long
// positional argument list.
type SeedDeps struct {
	// Client is the live engine client every pass reads from.
	Client suwayomi.Client
	// DB is the shared Ent client every pass writes its captured topology into.
	DB *ent.Client
	// Cache is the SHARED apk byte cache (constructed once in main.go and also
	// held by the /internal apk-serving handler) SeedExtensions caches into.
	Cache *apkcache.Store
	// Settings is the runtime-settings read+write surface SeedEngineConfig
	// gap-fills the engine's FlareSolverr + SOCKS config into
	// (*settings.Service satisfies it).
	Settings SettingsStore
	// HTTPGet fetches repo indexes + .apk bytes for SeedExtensions (http.Get in
	// production; a stub in tests).
	HTTPGet func(url string) (*http.Response, error)
}

// SeedReport is the aggregate outcome of one RunSeed pass — the per-pass counts
// rolled up so the boot goroutine can emit a single structured log line and a
// test can assert the passes actually ran. Skipped is true when the engine was
// unreachable and every pass was skipped (every other field then zero).
type SeedReport struct {
	Skipped            bool
	URLsFilled         int
	URLsRemaining      int
	Repos              int
	ExtensionsCached   int
	ExtensionGaps      int
	PrefsSeeded        int
	PrefSourcesSkipped int
}

// RunSeed runs the four one-shot engine-topology seed passes in order —
// BackfillProviderURLs → SeedExtensions → SeedSourcePreferences →
// SeedEngineConfig — and returns (and logs) their aggregate outcome. It is the
// boot-time wiring entry point: main.go launches it in a detached, non-blocking
// background goroutine once the engine is up, so a slow or failing seed can never
// delay the HTTP server or app boot.
//
// Contract:
//   - PANIC-SAFE: a deferred recover turns any seed bug into a logged error, so a
//     panic in one pass can never crash the process it is a background goroutine of.
//   - REACHABILITY-GATED: it first probes the engine (client.Sources); if that
//     fails the engine is not up, so every pass is SKIPPED (Skipped=true) and a
//     later boot retries — every pass is idempotent gap-fill, so nothing is lost
//     by skipping. This avoids a wall of per-row failures against a dead engine.
//   - FAULT-ISOLATED: each pass's error is logged and does NOT abort the others
//     (the passes are independent — a dead extension repo must not stop the
//     source-preference or engine-config seed). BackfillProviderURLs and
//     SeedSourcePreferences never fail the whole pass on a per-row/per-source
//     error (they log+count internally); SeedExtensions/SeedEngineConfig errors
//     are logged here and the next pass still runs.
//   - IDEMPOTENT: safe to re-run on every boot; a fully-seeded library does no
//     work and makes no writes (see each pass's own idempotency doc).
func RunSeed(ctx context.Context, deps SeedDeps) SeedReport {
	var report SeedReport
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "enginetopo: seed pass panicked — recovered", "panic", r)
		}
	}()

	if _, err := deps.Client.Sources(ctx); err != nil {
		slog.WarnContext(ctx, "enginetopo: engine unreachable, skipping topology seed (a later boot retries)", "err", err)
		report.Skipped = true
		return report
	}

	report.URLsFilled, report.URLsRemaining = runBackfill(ctx, deps)
	report.Repos, report.ExtensionsCached, report.ExtensionGaps = runSeedExtensions(ctx, deps)
	report.PrefsSeeded, report.PrefSourcesSkipped = runSeedPrefs(ctx, deps)
	runSeedConfig(ctx, deps)

	slog.InfoContext(ctx, "enginetopo: topology seed complete",
		"urls_filled", report.URLsFilled,
		"urls_remaining", report.URLsRemaining,
		"repos", report.Repos,
		"extensions_cached", report.ExtensionsCached,
		"extension_gaps", report.ExtensionGaps,
		"prefs_seeded", report.PrefsSeeded,
		"pref_sources_skipped", report.PrefSourcesSkipped,
	)
	return report
}

// runBackfill runs the SeriesProvider.url backfill, logging a query-level failure
// (the only error it returns) and reporting zero fills in that case.
func runBackfill(ctx context.Context, deps SeedDeps) (filled, remaining int) {
	filled, remaining, err := BackfillProviderURLs(ctx, deps.Client, deps.DB)
	if err != nil {
		slog.ErrorContext(ctx, "enginetopo: backfill provider urls failed", "err", err)
		return 0, 0
	}
	return filled, remaining
}

// runSeedExtensions runs the extension/repo/apk-cache seed, logging an
// enumerating-call failure and reporting zeroes in that case.
func runSeedExtensions(ctx context.Context, deps SeedDeps) (repos, cached, gaps int) {
	res, err := SeedExtensions(ctx, deps.Client, deps.DB, deps.Cache, deps.HTTPGet)
	if err != nil {
		slog.ErrorContext(ctx, "enginetopo: seed extensions failed", "err", err)
		return 0, 0, 0
	}
	return res.Repos, res.Cached, res.Gaps
}

// runSeedPrefs runs the per-source preference seed, logging a query-level failure
// and reporting zeroes in that case.
func runSeedPrefs(ctx context.Context, deps SeedDeps) (seeded, skipped int) {
	res, err := SeedSourcePreferences(ctx, deps.Client, deps.DB)
	if err != nil {
		slog.ErrorContext(ctx, "enginetopo: seed source preferences failed", "err", err)
		return 0, 0
	}
	return res.Seeded, res.SkippedSources
}

// runSeedConfig runs the engine-config (FlareSolverr + SOCKS) seed, logging any
// failure — it has no counts to report.
func runSeedConfig(ctx context.Context, deps SeedDeps) {
	if err := SeedEngineConfig(ctx, deps.Client, deps.Settings); err != nil {
		slog.ErrorContext(ctx, "enginetopo: seed engine config failed", "err", err)
	}
}
