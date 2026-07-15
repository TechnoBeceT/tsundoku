// Package enginetopo holds the durable engine-topology store: one-shot,
// boot-time capture passes (SeedExtensions, SeedSourcePreferences — see
// RunSeed), their DB->engine inverse (Reconcile — the recovery core for a
// wiped/swapped/rebuilt engine), a live best-effort write-through
// (OnExtensionInstalled/OnExtensionUninstalled/OnReposSet,
// WriteThroughEngineConfig), and a read-only status snapshot (TopologyStatus).
// These are one-shot/owner-triggered maintenance passes, as opposed to the
// recurring per-cycle work in internal/refresh/internal/download.
//
// (QCAT-253, P2 Suwayomi-removal slice 5): the extension/repo/preference
// passes target internal/sourceengine (the engine-host client). The
// SeriesProvider.url backfill and the engine-config gap-fill seed are
// RETIRED — see RunSeed's doc comment.
package enginetopo

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// SeedDeps bundles the collaborators the one-shot topology seed needs. It is a
// plain struct so main.go can wire the shared, construct-once instances (the
// same engine client, ent client, and apkcache store the rest of the app uses)
// into RunSeed's single background call without a long positional argument
// list.
type SeedDeps struct {
	// Client is the live engine client every pass reads from.
	Client sourceengine.Client
	// DB is the shared Ent client every pass writes its captured topology into.
	DB *ent.Client
	// Cache is the SHARED apk byte cache (constructed once in main.go and also
	// held by the /internal apk-serving handler) SeedExtensions caches into.
	Cache *apkcache.Store
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
	Repos              int
	ExtensionsCached   int
	ExtensionGaps      int
	PrefsSeeded        int
	PrefSourcesSkipped int
}

// RunSeed runs the two remaining one-shot engine-topology seed passes in order
// — SeedExtensions → SeedSourcePreferences — and returns (and logs) their
// aggregate outcome. It is the boot-time wiring entry point: main.go launches
// it in a detached, non-blocking background goroutine once the engine is up,
// so a slow or failing seed can never delay the HTTP server or app boot.
//
// (QCAT-253, P2 Suwayomi-removal slice 5): the SeriesProvider.url backfill and
// the engine-config gap-fill seed are RETIRED — sourceengine.Client has no
// id->url lookup (ingest sets url at write time now) and no readable engine
// config to gap-fill from (Tsundoku owns config outright, see reconcile.go's
// reconcileConfig). Their counts (URLsFilled/URLsRemaining) are gone from
// SeedReport accordingly.
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
//     source-preference seed). SeedSourcePreferences never fails the whole pass
//     on a per-source error (it logs+counts internally); SeedExtensions errors
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

	report.Repos, report.ExtensionsCached, report.ExtensionGaps = runSeedExtensions(ctx, deps)
	report.PrefsSeeded, report.PrefSourcesSkipped = runSeedPrefs(ctx, deps)

	slog.InfoContext(ctx, "enginetopo: topology seed complete",
		"repos", report.Repos,
		"extensions_cached", report.ExtensionsCached,
		"extension_gaps", report.ExtensionGaps,
		"prefs_seeded", report.PrefsSeeded,
		"pref_sources_skipped", report.PrefSourcesSkipped,
	)
	return report
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
