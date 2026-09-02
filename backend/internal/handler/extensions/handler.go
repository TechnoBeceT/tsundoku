// Package extensions holds the thin HTTP handlers for the engine host's
// "Sources & Extensions management" proxy. It lets the owner list, install,
// update, uninstall, and refresh extensions (the Tachiyomi/Mihon source
// plugins), manage the extension repo URL list, and explicitly approve repository
// signer pins — all from Tsundoku, so they never need direct access to the engine host.
//
// Most operations are passthroughs, but update is deliberately coordinated
// with Tsundoku's durable extension archive and provider references. It locks
// the provider table while the prepared candidate is activated, preventing a
// source identity still used by the library from disappearing mid-decision.
//
// Engine-host prepares and validates a complete prospective source/installed
// generation while readers continue using the previous generation. It persists
// the replacement manifest before publishing that generation with one atomic
// reference swap, and retires old files only afterward. A pre-publication
// registration, invariant, or manifest failure removes the candidate and leaves
// readers on the old generation. The handler therefore remains a pure
// passthrough; no post-mutation reload RPC or topology reconciliation is needed.
package extensions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/pkg/providerid"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcepurge"
)

// SourceToggleStore is the narrow surface the Handler needs for the TSUNDOKU-SIDE
// per-source enable/disable toggle (the Configure dialog's per-source Switch).
// It reads the disabled-source set (for the Preferences group `enabled` field)
// and writes one source's state (SetSourceEnabled). *disabledsource.Service
// satisfies it. A nil store (focused proxy tests) makes every group report
// enabled=true and SetSourceEnabled a no-op-if-unwired path (see its doc).
//
// The ROUTE and its DTO are unchanged by QCAT-513; what changed is what
// CONSUMING the flag does. Turning the switch off is now a FULL PAUSE of the
// source — refresh stops polling it and the dispatcher stops downloading and
// upgrading from it — not merely a picker filter. See the internal/disabledsource
// package doc for the exact scope, and for what a pause deliberately does NOT do
// (it deletes nothing and re-ranks nothing).
type SourceToggleStore interface {
	Disabled(ctx context.Context) (map[int64]bool, error)
	// DisabledSince maps each paused source id to when it was paused (its row's
	// immutable created_at) — the "paused since" timestamp the Configure dialog
	// renders. Same membership as Disabled; absence means active.
	DisabledSince(ctx context.Context) (map[int64]time.Time, error)
	SetEnabled(ctx context.Context, sourceID int64, enabled bool) error
}

// IgnoreScanlatorStore is the narrow surface the Handler needs for the
// TSUNDOKU-SIDE per-source "ignore scanlator" flag (the Configure dialog's
// per-source toggle, sibling of the enable/disable Switch). It reads the flagged
// set (for the Preferences group's ignoreScanlator field) and writes one
// source's flag (SetSourceIgnoreScanlator). *ignorescanlator.Service satisfies
// it. A nil store (focused proxy tests) makes every group report
// ignoreScanlator=false and SetSourceIgnoreScanlator a no-op-if-unwired path.
type IgnoreScanlatorStore interface {
	IgnoreScanlatorSet(ctx context.Context) (map[int64]bool, error)
	SetIgnore(ctx context.Context, sourceID int64, ignore bool) error
}

// ScanlatorCollapser runs the Slice-B on-enable migration when a source is
// flagged ignore-scanlator ON: it folds that source's already-adopted
// per-uploader SeriesProvider rows across ALL series into one [Source] provider
// and relabels the affected CBZs (see library.Service.CollapseIgnoredScanlatorSource).
// It returns how many series were collapsed, how many per-uploader rows were
// folded, and how many series were left for a re-run (an error, or another merge
// already holding that series' merge latch — GAP-122). *library.Service
// satisfies it. A nil collapser (focused proxy tests, or a deployment without the
// library service) makes flip-ON persist the flag WITHOUT migrating existing
// series — the apply-forward Slice-A behaviour, still correct.
type ScanlatorCollapser interface {
	CollapseIgnoredScanlatorSource(ctx context.Context, sourceID int64) (seriesProcessed, merged, skipped int, err error)
}

// Handler serves the extension-management endpoints. It holds the engine-host
// client, plus the durable engine-topology store (Ent client + apk byte cache
// + an httpGet for repo indexes/.apk downloads) that the best-effort
// write-through captures every install/update/uninstall/repo change into, plus
// the Tsundoku-side per-source disabled-flag store for the enable/disable toggle.
type Handler struct {
	sw sourceengine.Client
	// db and cache are the durable topology store the write-through updates. When
	// EITHER is nil the write-through is a no-op and the handler is a pure proxy —
	// used where topology capture is not wired, e.g. focused proxy tests.
	db      *ent.Client
	cache   *apkcache.Store
	httpGet func(url string) (*http.Response, error)
	// disabled is the Tsundoku-side per-source enable/disable store. Nil ⇒ every
	// group reports enabled=true and the enable/disable route is unavailable.
	disabled SourceToggleStore
	// ignoreScanlator is the Tsundoku-side per-source ignore-scanlator flag store.
	// Nil ⇒ every group reports ignoreScanlator=false and the flag route is
	// unavailable.
	ignoreScanlator IgnoreScanlatorStore
	// collapser runs the Slice-B on-enable migration (fold existing per-uploader
	// providers + relabel CBZs) when a source is flagged ON. Nil ⇒ flip-ON only
	// persists the flag (apply-forward, Slice A) without migrating existing series.
	// Attach it with WithScanlatorCollapser (wired after the library service is
	// constructed — see server/routes.go).
	collapser ScanlatorCollapser
	// retained resolves the apk-cache rollback-history depth
	// (extensions.retained_versions) at use-time — the prune count for the
	// install/update write-through and the reinstall path. Nil ⇒ the built-in
	// default (enginetopo.defaultRetainedVersions).
	retained func(context.Context) int
	// purge cascades the DB cleanup (dangling SeriesProviders + feeds, the
	// source's metric + breaker rows) for an uninstalled extension's now-orphaned
	// sources — keeping every CBZ (never-auto-delete). Nil ⇒ uninstall does NOT
	// auto-cascade (pure passthrough; focused proxy tests). Attach with WithPurge.
	purge extensionPurger
	// archive is the shared exact-generation capture service. When wired, Update
	// must archive both the pre-mutation and returned post-mutation generations.
	archive *enginetopo.ExtensionArchive
	// beforeUpdateCommit is a deterministic commit-failure seam used by the
	// package's real-PostgreSQL transaction test. Nil in production.
	beforeUpdateCommit func(*ent.Tx)
}

// WithArchive attaches the shared exact installed-generation archive used by
// boot seeding and fail-closed extension updates.
func (h *Handler) WithArchive(archive *enginetopo.ExtensionArchive) *Handler {
	h.archive = archive
	return h
}

// extensionPurger is the narrow surface the uninstall auto-cascade needs — the
// pkgName→sources purge. *sourcepurge.Service satisfies it. A narrow port keeps
// the extension handler from importing the whole purge package surface and lets
// tests supply a lightweight fake.
type extensionPurger interface {
	PurgeExtension(ctx context.Context, pkgName string) (sourcepurge.ExtensionSummary, error)
}

// NewHandler constructs a Handler bound to an engine client, the durable
// engine-topology store (Ent client, apk cache, and the httpGet used to fetch
// repo indexes + .apk bytes — http.Get in production), the Tsundoku-side
// per-source disabled-flag store, and the Tsundoku-side per-source
// ignore-scanlator flag store. db/cache/httpGet may be nil, which turns the
// write-through into a no-op (pure passthrough); disabled may be nil, which
// makes every group enabled and disables the enable/disable route;
// ignoreScanlator may be nil, which makes every group report
// ignoreScanlator=false and disables the ignore-scanlator route.
func NewHandler(
	sw sourceengine.Client,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
	disabled SourceToggleStore,
	ignoreScanlator IgnoreScanlatorStore,
	retained func(context.Context) int,
) *Handler {
	return &Handler{sw: sw, db: db, cache: cache, httpGet: httpGet, disabled: disabled, ignoreScanlator: ignoreScanlator, retained: retained}
}

// WithScanlatorCollapser attaches the Slice-B on-enable migration runner so
// flagging a source ignore-scanlator ON also folds its already-adopted
// per-uploader providers + relabels their CBZs (see ScanlatorCollapser). It is a
// setter (not a NewHandler param) because the library service that satisfies it
// is constructed AFTER this handler in server/routes.go — the route closures
// capture the *Handler pointer, so setting the field afterwards is safe. Returns
// the receiver for chaining.
func (h *Handler) WithScanlatorCollapser(collapser ScanlatorCollapser) *Handler {
	h.collapser = collapser
	return h
}

// WithPurge attaches the source-purge service so that uninstalling an extension
// also CASCADE-purges all of Tsundoku's DB state for its now-orphaned source(s)
// (see Uninstall). Like WithScanlatorCollapser it is a setter (not a NewHandler
// param) because the purge service is constructed alongside the other library
// services in server/routes.go. Returns the receiver for chaining.
func (h *Handler) WithPurge(purge extensionPurger) *Handler {
	h.purge = purge
	return h
}

// retainedCount resolves the rollback-history depth at use-time, falling back to
// the shared default when no resolver is wired (focused proxy tests).
func (h *Handler) retainedCount(ctx context.Context) int {
	if h.retained == nil {
		return defaultRetainedVersions
	}
	if n := h.retained(ctx); n >= 1 {
		return n
	}
	return defaultRetainedVersions
}

// defaultRetainedVersions mirrors settings' extensions.retained_versions default
// (3) for the unwired-resolver path — kept in sync with config's default.
const defaultRetainedVersions = 3

// respondExtensions writes the extension list as JSON, attaching each package's
// held-version set from ONE batched read of the durable store (no N+1). The map
// is built from a single HarvestedExtension query regardless of list size; when
// the store is not wired it is empty (every extension reports cachedVersions:[]).
func (h *Handler) respondExtensions(c echo.Context, exts []sourceengine.Extension) error {
	held := h.heldVersionsByPkg(c.Request().Context())
	return c.JSON(http.StatusOK, toExtensionDTOs(exts, held))
}

// heldVersionsByPkg loads the held (retained) .apk versions for every extension
// in ONE query (pkg_name → cached_versions), so respondExtensions never issues a
// per-extension lookup. A nil store or a read failure yields an empty map — the
// held-version list is a display enrichment, never a reason to fail the list.
func (h *Handler) heldVersionsByPkg(ctx context.Context) map[string][]apkcache.CachedVersion {
	if h.db == nil {
		return map[string][]apkcache.CachedVersion{}
	}
	rows, err := h.db.HarvestedExtension.Query().All(ctx)
	if err != nil {
		slog.WarnContext(ctx, "extensions: could not load held versions, omitting", "err", err)
		return map[string][]apkcache.CachedVersion{}
	}
	byPkg := make(map[string][]apkcache.CachedVersion, len(rows))
	for _, r := range rows {
		byPkg[r.PkgName] = r.CachedVersions
	}
	return byPkg
}

// List handles GET /api/suwayomi/extensions. It returns every extension
// (installed + available). An upstream failure is a 502.
func (h *Handler) List(c echo.Context) error {
	exts, err := h.sw.Extensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return h.respondExtensions(c, exts)
}

// Refresh handles POST /api/suwayomi/extensions/refresh. It re-fetches the
// available-extensions list from the configured repos ("check for updates") and
// returns the refreshed list. An upstream failure is a 502.
func (h *Handler) Refresh(c echo.Context) error {
	exts, err := h.sw.RefreshExtensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return h.respondExtensions(c, exts)
}

// Install handles POST /api/suwayomi/extensions/:pkgName/install. It installs
// REPO-based (apkURL ""; the apk-cache fallback + sideload install is
// DEFERRED — see enginetopo.Reconcile's doc comment on the same deferral).
func (h *Handler) Install(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	exts, err := h.sw.Extensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	for _, ext := range exts {
		if ext.PkgName == pkgName && ext.IsInstalled {
			return echo.NewHTTPError(http.StatusConflict, "extension is already installed; use the protected update operation")
		}
	}
	return h.mutate(c, func(ctx context.Context, pkgName string) ([]sourceengine.Extension, error) {
		return h.sw.InstallExtension(ctx, pkgName, "")
	})
}

// Update handles POST /api/suwayomi/extensions/:pkgName/update.
func (h *Handler) Update(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	if h.archive == nil {
		return httperr.Upstream(errors.New("protected extension update unavailable"))
	}
	ctx := c.Request().Context()
	updater, err := sourceengine.ProtectedUpdaterFor(h.sw)
	if err != nil {
		return httperr.Upstream(err)
	}
	exts, mutated, err := h.archive.UpdateWith(ctx, pkgName, func() enginetopo.ExtensionUpdateOutcome {
		prepared, err := updater.PrepareExtensionUpdate(ctx, pkgName)
		if err != nil {
			return enginetopo.ExtensionUpdateOutcome{Degradation: err}
		}
		succeeded := false
		defer func() {
			if !succeeded {
				discardCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if discardErr := updater.DiscardPreparedExtensionUpdate(discardCtx, pkgName, prepared.Token); discardErr != nil {
					slog.WarnContext(ctx, "extensions: discard prepared update failed", "pkg_name", pkgName, "err", discardErr)
				}
			}
		}()
		tx, err := h.db.Tx(ctx)
		if err != nil {
			return enginetopo.ExtensionUpdateOutcome{Degradation: err}
		}
		defer func() { _ = tx.Rollback() }()
		protected, providers, series, err := referencedRemovedSources(ctx, tx, prepared.RemovedSourceIDs)
		if err != nil {
			return enginetopo.ExtensionUpdateOutcome{Degradation: err}
		}
		result, err := updater.ActivatePreparedExtensionUpdate(ctx, sourceengine.ActivatePreparedExtensionUpdate{PreparedExtensionUpdate: prepared, ProtectedSourceIDs: protected})
		if err != nil {
			var conflict *sourceengine.SourceRetirementConflictError
			if errors.As(err, &conflict) {
				return enginetopo.ExtensionUpdateOutcome{Degradation: &retirementConflict{sourceIDs: conflict.SourceIDs, providerCount: providers, seriesCount: series}}
			}
			// A response can be lost after the host has atomically published the new
			// generation. Reconcile the durable token outcome before deciding whether
			// discard or retry is safe.
			reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			outcome, outcomeErr := updater.PreparedExtensionUpdateOutcome(reconcileCtx, pkgName, prepared.Token)
			if outcomeErr == nil && outcome.Status == "rejected" {
				return enginetopo.ExtensionUpdateOutcome{Degradation: err}
			}
			result, listErr := h.sw.Extensions(reconcileCtx)
			if outcomeErr == nil && outcome.Status == "committed" {
				succeeded = true
				return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: errors.Join(err, listErr)}
			}
			if current, ok := findExtension(result, pkgName); ok && current.IsInstalled && current.VersionCode == prepared.CandidateVersionCode {
				succeeded = true
				return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: errors.Join(err, outcomeErr, listErr)}
			}
			// pending/unknown (or an unavailable outcome query) is intentionally
			// reported as a non-retryable ambiguous success. The durable pre-capture
			// remains available for explicit operator reconciliation.
			succeeded = true
			return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: &activationAmbiguous{pkgName: pkgName, candidateVersion: prepared.CandidateVersionCode, cause: errors.Join(err, outcomeErr, listErr)}}
		}
		// Activation consumes the token and crosses the irreversible boundary.
		succeeded = true
		if h.beforeUpdateCommit != nil {
			h.beforeUpdateCommit(tx)
		}
		if err := tx.Commit(); err != nil {
			return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: fmt.Errorf("provider-reference coordination commit after successful activation: %w", err)}
		}
		return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true}
	})
	if err != nil && !mutated {
		var conflict *retirementConflict
		if errors.As(err, &conflict) {
			return c.JSON(http.StatusConflict, map[string]any{"message": "extension update would retire sources used by the library", "code": "source_retirement_conflict", "pkgName": pkgName, "sourceIds": sourceIDStrings(conflict.sourceIDs), "affectedProviderCount": conflict.providerCount, "affectedSeriesCount": conflict.seriesCount})
		}
		return httperr.Upstream(err)
	}
	if err != nil {
		var ambiguous *activationAmbiguous
		if errors.As(err, &ambiguous) {
			slog.ErrorContext(ctx, "extensions: activation outcome ambiguous; do not retry", "pkg_name", pkgName, "candidate_version", ambiguous.candidateVersion, "err", err)
			return c.JSON(http.StatusAccepted, map[string]any{"message": "extension activation outcome is ambiguous; do not retry", "code": "activation_outcome_ambiguous", "pkgName": pkgName, "candidateVersionCode": ambiguous.candidateVersion})
		}
		slog.ErrorContext(ctx, "extensions: update succeeded with local coordination or archive degradation; do not retry", "pkg_name", pkgName, "err", err)
	}
	return h.respondExtensions(c, exts)
}

type activationAmbiguous struct {
	pkgName          string
	candidateVersion int64
	cause            error
}

func (e *activationAmbiguous) Error() string { return "extension activation outcome is ambiguous" }
func (e *activationAmbiguous) Unwrap() error { return e.cause }

func sourceIDStrings(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}

type retirementConflict struct {
	sourceIDs                  []int64
	providerCount, seriesCount int
}

func (e *retirementConflict) Error() string { return "extension update would retire protected sources" }

func referencedRemovedSources(ctx context.Context, tx *ent.Tx, removed []int64) ([]int64, int, int, error) {
	removedSet := make(map[int64]struct{}, len(removed))
	for _, id := range removed {
		removedSet[id] = struct{}{}
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE series_providers IN SHARE MODE`); err != nil {
		return nil, 0, 0, err
	}
	rows, err := tx.SeriesProvider.Query().All(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	ids := map[int64]struct{}{}
	series := map[string]struct{}{}
	providers := 0
	for _, row := range rows {
		id, live := providerid.SourceID(row.Provider)
		if !live {
			continue
		}
		if _, retiring := removedSet[id]; !retiring {
			continue
		}
		ids[id] = struct{}{}
		providers++
		series[row.SeriesID.String()] = struct{}{}
	}
	protected := make([]int64, 0, len(ids))
	for id := range ids {
		protected = append(protected, id)
	}
	sort.Slice(protected, func(i, j int) bool { return protected[i] < protected[j] })
	return protected, providers, len(series), nil
}

// Uninstall handles DELETE /api/suwayomi/extensions/:pkgName. It skips the
// shared install/update write-through capture (captureInstallOrUpdate) —
// uninstall removes the durable row + cached apk instead (OnExtensionUninstalled).
//
// It ALSO cascade-purges all of Tsundoku's DB state for the extension's
// now-orphaned source(s) (dangling SeriesProviders + feeds, their metric + breaker
// rows), keeping every CBZ (never-auto-delete), so the owner no longer has to
// clean those up series-by-series by hand. The purge runs BEFORE
// OnExtensionUninstalled because the purge reads the extension's source ids from
// the HarvestedExtension row that OnExtensionUninstalled then deletes. Best-effort:
// a purge failure is logged, never fails the uninstall (mirrors the write-through's
// own best-effort discipline).
func (h *Handler) Uninstall(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	exts, err := h.sw.UninstallExtension(ctx, pkgName)
	if err != nil {
		return httperr.Upstream(err)
	}
	if h.purge != nil {
		if sum, pErr := h.purge.PurgeExtension(ctx, pkgName); pErr != nil {
			slog.WarnContext(ctx, "extensions: uninstall purge cascade failed (DB state may be left behind)",
				"pkg_name", pkgName, "err", pErr)
		} else if sum.ProvidersRemoved > 0 || sum.MetricsDeleted > 0 || sum.BreakerCleared > 0 {
			slog.InfoContext(ctx, "extensions: purged orphaned source state after uninstall",
				"pkg_name", pkgName, "seriesAffected", sum.SeriesAffected, "providersRemoved", sum.ProvidersRemoved,
				"chaptersDeleted", sum.ChaptersDeleted, "metricsDeleted", sum.MetricsDeleted, "breakerCleared", sum.BreakerCleared,
				"errors", sum.Errors)
		}
	}
	if h.db != nil {
		enginetopo.OnExtensionUninstalled(ctx, h.db, h.cache, pkgName)
	}
	return h.respondExtensions(c, exts)
}

// Reinstall handles POST /api/suwayomi/extensions/:pkgName/reinstall — the
// reversible-update rollback path. It reinstalls a HELD (older) .apk version
// from Tsundoku's own apk cache, addressed by (pkgName, versionCode) in the body.
//
// The engine host prepares it BY LOCAL FILESYSTEM PATH: the engine host and the
// Go server run in the SAME container sharing the /config volume the apk cache
// lives on, and the engine host's install(apkUrl) treats a NON-http apkUrl as a
// local file it copies onto its own volume — an EXISTING engine capability
// (ExtensionManager.install / ExtensionLoader.resolveApk), so no HTTP fetch and
// no auth are involved. (The /internal apk HTTP route stays owner-authed and is
// deliberately NOT used here: the engine fetches an http URL with no auth headers
// and would 401 against it — see the DISCOVERY note in the reversible-updates
// feature. The local-path install is the correct wiring for the bundled
// single-container topology; a remote/external engine that does not share the
// filesystem is a documented follow-up.)
//
// Flow: validate → require HELD metadata and bytes → exact pre-capture → prepare
// the cached APK once → hold the provider table against concurrent references →
// activate the source-ID witness → reconcile any lost response → exact
// post-capture. A protected retired source is 409; an ambiguous activation is
// explicit 202 and must not be retried.
func (h *Handler) Reinstall(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	if h.db == nil || h.cache == nil {
		// The reinstall path needs the durable store + apk cache; without them there
		// is no held-version history to reinstall from.
		return echo.NewHTTPError(http.StatusServiceUnavailable, "extension version cache not available")
	}
	var req ReinstallRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	versionCode, err := validateReinstall(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if !h.heldVersionOnDisk(ctx, pkgName, versionCode) {
		return echo.NewHTTPError(http.StatusNotFound, "no cached apk for that extension version")
	}

	if h.archive == nil {
		return httperr.Upstream(errors.New("protected extension reinstall unavailable"))
	}
	updater, err := sourceengine.ProtectedUpdaterFor(h.sw)
	if err != nil {
		return httperr.Upstream(err)
	}
	return h.runProtectedReinstall(c, updater, pkgName, versionCode, h.cache.Path(pkgName, versionCode))
}

func (h *Handler) runProtectedReinstall(c echo.Context, updater sourceengine.ProtectedExtensionUpdater, pkgName string, versionCode int, apkPath string) error {
	ctx := c.Request().Context()
	exts, mutated, err := h.archive.UpdateWith(ctx, pkgName, func() enginetopo.ExtensionUpdateOutcome {
		prepared, err := updater.PrepareExtensionReinstall(ctx, sourceengine.PrepareExtensionReinstall{PkgName: pkgName, APKURL: apkPath, CandidateVersionCode: int64(versionCode)})
		if err != nil {
			return enginetopo.ExtensionUpdateOutcome{Degradation: err}
		}
		succeeded := false
		defer func() {
			if !succeeded {
				discardCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if discardErr := updater.DiscardPreparedExtensionUpdate(discardCtx, pkgName, prepared.Token); discardErr != nil {
					slog.WarnContext(ctx, "extensions: discard prepared reinstall failed", "pkg_name", pkgName, "err", discardErr)
				}
			}
		}()
		tx, err := h.db.Tx(ctx)
		if err != nil {
			return enginetopo.ExtensionUpdateOutcome{Degradation: err}
		}
		defer func() { _ = tx.Rollback() }()
		protected, providers, series, err := referencedRemovedSources(ctx, tx, prepared.RemovedSourceIDs)
		if err != nil {
			return enginetopo.ExtensionUpdateOutcome{Degradation: err}
		}
		result, err := updater.ActivatePreparedExtensionUpdate(ctx, sourceengine.ActivatePreparedExtensionUpdate{PreparedExtensionUpdate: prepared, ProtectedSourceIDs: protected})
		if err != nil {
			var conflict *sourceengine.SourceRetirementConflictError
			if errors.As(err, &conflict) {
				return enginetopo.ExtensionUpdateOutcome{Degradation: &retirementConflict{sourceIDs: conflict.SourceIDs, providerCount: providers, seriesCount: series}}
			}
			reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			outcome, outcomeErr := updater.PreparedExtensionUpdateOutcome(reconcileCtx, pkgName, prepared.Token)
			if outcomeErr == nil && outcome.Status == "rejected" {
				return enginetopo.ExtensionUpdateOutcome{Degradation: err}
			}
			result, listErr := h.sw.Extensions(reconcileCtx)
			if outcomeErr == nil && outcome.Status == "committed" {
				succeeded = true
				return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: errors.Join(err, listErr)}
			}
			if current, ok := findExtension(result, pkgName); ok && current.IsInstalled && current.VersionCode == prepared.CandidateVersionCode {
				succeeded = true
				return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: errors.Join(err, outcomeErr, listErr)}
			}
			succeeded = true
			return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: &activationAmbiguous{pkgName: pkgName, candidateVersion: prepared.CandidateVersionCode, cause: errors.Join(err, outcomeErr, listErr)}}
		}
		succeeded = true
		if err := tx.Commit(); err != nil {
			return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true, Degradation: fmt.Errorf("provider-reference coordination commit after successful reinstall: %w", err)}
		}
		return enginetopo.ExtensionUpdateOutcome{Extensions: result, Activated: true}
	})
	if err != nil && !mutated {
		var conflict *retirementConflict
		if errors.As(err, &conflict) {
			return c.JSON(http.StatusConflict, map[string]any{"message": "extension reinstall would retire sources used by the library", "code": "source_retirement_conflict", "pkgName": pkgName, "sourceIds": sourceIDStrings(conflict.sourceIDs), "affectedProviderCount": conflict.providerCount, "affectedSeriesCount": conflict.seriesCount})
		}
		return httperr.Upstream(err)
	}
	if err != nil {
		var ambiguous *activationAmbiguous
		if errors.As(err, &ambiguous) {
			return c.JSON(http.StatusAccepted, map[string]any{"message": "extension activation outcome is ambiguous; do not retry", "code": "activation_outcome_ambiguous", "pkgName": pkgName, "candidateVersionCode": ambiguous.candidateVersion})
		}
		slog.ErrorContext(ctx, "extensions: reinstall succeeded with archive degradation; do not retry", "pkg_name", pkgName, "err", err)
	}
	return h.respondExtensions(c, exts)
}

// heldVersionOnDisk reports whether versionCode is recorded in pkgName's held set
// AND its .apk bytes are present in the cache — BOTH are required for a reinstall
// (the DB row is the durable claim; the file is the actual bytes the engine
// installs). Reuses the single-query held-versions load (no per-request N+1).
func (h *Handler) heldVersionOnDisk(ctx context.Context, pkgName string, versionCode int) bool {
	found := false
	for _, cv := range h.heldVersionsByPkg(ctx)[pkgName] {
		if cv.VersionCode == versionCode {
			found = true
			break
		}
	}
	return found && h.cache.Exists(pkgName, versionCode)
}

// mutate is the shared body of Install/Update: it validates :pkgName, applies
// the mutation (which the engine host ALREADY returns the refreshed extension
// list from — unlike the retired Suwayomi shape, no separate re-read call is
// needed), captures the just-affected extension into the durable topology
// store (best-effort write-through), and returns the refreshed list. A
// validation failure is a 400; an upstream failure is a 502.
func (h *Handler) mutate(
	c echo.Context,
	apply func(ctx context.Context, pkgName string) ([]sourceengine.Extension, error),
) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	exts, err := apply(ctx, pkgName)
	if err != nil {
		return httperr.Upstream(err)
	}
	h.captureInstallOrUpdate(ctx, pkgName, exts)
	return h.respondExtensions(c, exts)
}

// captureInstallOrUpdate runs the best-effort durable write-through after a
// successful install/update engine mutation: it finds the just-affected
// extension by pkgName in the handler's post-mutation exts (no redundant
// Extensions() call), then captures it via OnExtensionInstalled. A no-op when
// the durable store is not wired, or when pkgName is absent from the refreshed
// list (logged and skipped). Any capture failure is logged inside the
// enginetopo helpers and never affects the handler's success response.
func (h *Handler) captureInstallOrUpdate(ctx context.Context, pkgName string, exts []sourceengine.Extension) {
	if h.db == nil || h.cache == nil {
		return
	}
	ext, ok := findExtension(exts, pkgName)
	if !ok {
		slog.WarnContext(ctx, "extensions: installed extension not in post-mutation list, skipping topology capture",
			"pkg_name", pkgName)
		return
	}
	if h.archive != nil {
		if err := h.archive.Capture(ctx, ext); err != nil {
			slog.ErrorContext(ctx, "extensions: install succeeded but exact archive degraded", "pkg_name", pkgName, "err", err)
		}
		return
	}
	enginetopo.OnExtensionInstalled(ctx, h.db, h.cache, func(_ context.Context, url string) (*http.Response, error) {
		return h.httpGet(url)
	}, ext, h.retainedCount(ctx))
}

// findExtension returns the extension with the given pkgName from exts.
func findExtension(exts []sourceengine.Extension, pkgName string) (sourceengine.Extension, bool) {
	for _, e := range exts {
		if e.PkgName == pkgName {
			return e, true
		}
	}
	return sourceengine.Extension{}, false
}

// GetRepos handles GET /api/suwayomi/extensions/repos. It returns the configured
// repo URL list. An upstream failure is a 502.
func (h *Handler) GetRepos(c echo.Context) error {
	repos, err := h.sw.Repos(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toReposDTO(repos))
}

// SetRepos handles PUT /api/suwayomi/extensions/repos. It validates the full
// replacement list, applies it (an empty array clears all repos, and the engine
// host echoes the written list back), then writes it through to the durable
// store and returns it (§16 round-trip). A validation failure is a 400; an
// upstream failure is a 502.
func (h *Handler) SetRepos(c echo.Context) error {
	var req ReposUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	repos, err := validateRepos(req)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	current, err := h.sw.SetRepos(ctx, repos)
	if err != nil {
		return httperr.Upstream(err)
	}
	// Best-effort durable write-through: replace the HarvestedRepo set with the
	// authoritative echoed-back list (rows for removed repos are pruned). Logged-
	// and-swallowed inside the helper; never affects this response.
	if h.db != nil {
		enginetopo.OnReposSet(ctx, h.db, current)
	}
	return c.JSON(http.StatusOK, toReposDTO(current))
}

// GetRepoTrust handles GET /api/suwayomi/extensions/repos/trust. The route is
// owner-only and returns the complete independently configured signer-pin map.
func (h *Handler) GetRepoTrust(c echo.Context) error {
	trust, err := h.sw.RepoTrust(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toRepoTrustDTO(trust))
}

// SetRepoTrust handles PUT /api/suwayomi/extensions/repos/trust. It validates
// the independent signer pin, asks the engine host to persist it atomically,
// and returns the complete map read back.
func (h *Handler) SetRepoTrust(c echo.Context) error {
	var req RepoTrustUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	repoURL, fingerprint, err := validateRepoTrust(req)
	if err != nil {
		return err
	}
	trust, err := h.sw.SetRepoTrust(c.Request().Context(), repoURL, fingerprint)
	if err != nil {
		var badRequest *sourceengine.BadRequestError
		if errors.As(err, &badRequest) {
			return httperr.BadRequest(badRequest.Msg)
		}
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toRepoTrustDTO(trust))
}
