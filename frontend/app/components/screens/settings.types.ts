/**
 * Prop/data types for the Settings screen — the single-owner control panel.
 *
 * These mirror the PLANNED Settings surface (see the Settings design brief):
 * a few runtime-editable Tsundoku knobs (the M12 allowlist), user-defined
 * categories, read-only engine info + the embedded-engine upgrade flow, the
 * Tsundoku-owned FlareSolverr config (QCAT-238), and extension/repo
 * management. Everything is presentation-only: the screen receives state via
 * props and emits every mutation — no fetching, routing, or stores. Kept in
 * this `.ts` (never exported from a `.vue`) so stories + fixtures import freely.
 */

/** The settings panes, selected from the sticky sidebar nav. */
export type SettingsPane = 'library' | 'categories' | 'engine' | 'serverConfig' | 'extensions' | 'sources' | 'network' | 'trackers' | 'notifications'

/** NotificationPermission is this device's honest Web Push status (Notifications pane). */
export type NotificationPermissionState = 'unsupported' | 'blocked' | 'granted' | 'default'

/** Duration unit for the number+unit inputs (serialises to Go `2h`/`15m`/`30s`). */
export type DurationUnit = 'h' | 'm' | 's'

/** A friendly duration: a number plus its unit (never a raw `2h` text box). */
export interface DurationValue {
  /** The scalar amount (clamped ≥ 0 by the inputs). */
  value: number
  /** The unit the amount is expressed in. */
  unit: DurationUnit
}

/** The §16 lifecycle of a per-pane save: drives the spinner + inline result. */
export type SaveStatus = 'idle' | 'saving' | 'success' | 'error'

/** A save's current state — `message` carries the error text when `status` is error. */
export interface SaveState {
  /** Where the save is in its loading → success/error lifecycle. */
  status: SaveStatus
  /** Human-readable detail, shown for the `error` (and optionally `success`) status. */
  message?: string
}

/**
 * RowActionState — the §16 state of per-row mutations on a list pane (categories,
 * extensions, repos). `busyId` is the id of the single row whose action is in
 * flight (its control spins + disables); `error` is a human-readable failure
 * surfaced inline. A create action (add category / add repo) reports its in-flight
 * row as `ADD_ACTION_ID`, since the new row has no id yet.
 */
export interface RowActionState {
  /** Id of the row currently mutating (or `ADD_ACTION_ID` for a create); null when idle. */
  busyId?: string | null
  /** A human-readable failure surfaced inline; empty/absent when none. */
  error?: string
}

/** Sentinel `RowActionState.busyId` for an in-flight create (add) action. */
export const ADD_ACTION_ID = '__add__'

/* ---- 2a. Library / app settings ------------------------------------------- */

/**
 * LibrarySettings — Tsundoku's own runtime-editable knobs (the M12 allowlist).
 * The job schedulers re-read these on the next tick.
 */
export interface LibrarySettings {
  /** How often to poll titles for new chapters. */
  refreshInterval: DurationValue
  /** Queue-drain & upgrade-swap cadence. */
  downloadInterval: DurationValue
  /** Wait before retrying a failed chapter. */
  retryBackoff: DurationValue
  /** Attempts per source before that source is abandoned; a chapter fails only when all its sources are exhausted. */
  maxRetries: number
  /** Health threshold (days) before a source counts as stale. */
  staleGraceDays: number
  /** Parallel source fetches — the "be gentle on sources" advanced knob. */
  refreshConcurrency: number
  /** Parallel downloads per source — how many of a source's chapters download at once. */
  downloadConcurrency: number
}

/** Read-only deploy-time facts shown in the System card (set via env vars). */
export interface SystemInfo {
  /** Library root path, e.g. `/data/manga`. */
  storageFolder: string
  /** HTTP server port. */
  serverPort: string
  /** Display string for the DB host/name (never the password). */
  database: string
}

/**
 * SourcesSettings — the anti-IP-block runtime knobs (source-politeness spec):
 * the warm-up job's cadence/threshold plus the per-source circuit-breaker and
 * politeness-delay tunables. Surfaced in the Sources pane alongside the
 * existing per-source search metrics.
 */
export interface SourcesSettings {
  /** How often to keep anti-bot source sessions warm; 0 disables. */
  warmupInterval: DurationValue
  /** EWMA-latency threshold (ms) above which WarmSlow treats a source as needing warming. */
  warmupSlowThresholdMs: number
  /** Consecutive failures before a source's circuit-breaker trips it into cooldown. */
  failureThreshold: number
  /** How long a tripped source's circuit-breaker stays open before it's tried again. */
  cooldown: DurationValue
  /** Minimum gap (ms) enforced between requests to one source; 0 disables. */
  minRequestDelayMs: number
}

/* ---- 2b. Categories ------------------------------------------------------- */

/**
 * SettingsCategory — one row in the user-definable category list. A `protected`
 * category (the default landing, e.g. "Other") cannot be renamed or deleted.
 */
export interface SettingsCategory {
  /** Category UUID (the mutation target). */
  id: string
  /** Display/folder name. */
  name: string
  /** How many series currently sit in this category. */
  count: number
  /** Whether new/uncategorised series land here (drives the DEFAULT pill). */
  isDefault: boolean
  /** Protected categories can't be renamed or deleted (and can't lose default). */
  protected: boolean
}

/* ---- 2c. Engine / Suwayomi management ------------------------------------- */

/** Which lifecycle mode the engine runs in (config-selected, read-only here). */
export type EngineMode = 'embedded' | 'external'

/** Per-step state of the embedded-engine upgrade stepper (SSE-driven, §17). */
export type UpgradeStepStatus = 'pending' | 'active' | 'done' | 'failed'

/** One step of the upgrade sequence (Stop → Backup → Swap → Migrate → Verify). */
export interface UpgradeStep {
  /** The step's human label. */
  label: string
  /** Where the step currently is in its lifecycle. */
  status: UpgradeStepStatus
}

/**
 * EngineInfo — read-only engine status. `mode` drives which rows show: external
 * mode shows only the URL; embedded shows version/dirs + the upgrade affordance.
 */
export interface EngineInfo {
  /** Embedded (Tsundoku-managed JAR) or external (owner-run instance). */
  mode: EngineMode
  /** The external base URL (external mode only). */
  externalUrl: string
  /** Currently running engine version (embedded mode). */
  runningVersion: string
  /** Pinned target version (embedded mode). */
  pinnedVersion: string
  /** Engine runtime dir diagnostic row (embedded mode). */
  runtimeDir: string
  /** Java binary path diagnostic row (embedded mode). */
  javaPath: string
  /** Process status indicator (embedded mode). */
  status: 'running' | 'stopped' | 'starting'
  /** Whether a newer pinned version is available to upgrade to. */
  upgradeAvailable: boolean
  /** The version an upgrade would move to (when `upgradeAvailable`). */
  availableVersion: string
}

/* ---- 2d. FlareSolverr config (Tsundoku-owned) ----------------------------- */

/**
 * Editable FlareSolverr (Cloudflare-bypass) config, gated behind the toggle.
 * Tsundoku-owned config served by its own endpoint (QCAT-238, 2026-07-14) —
 * the proxied Suwayomi server-config pane (SOCKS proxy + read-only DB
 * display) was RETIRED with the P2 Suwayomi-removal cutover (the engine host
 * has no such passthrough endpoint); this card is the only one left in
 * SuwayomiPane.vue.
 */
export interface FlareSolverrConfig {
  /** Solve Cloudflare challenges for protected sources when true. */
  enabled: boolean
  /** FlareSolverr server URL. */
  url: string
  /** Per-request timeout. */
  timeout: DurationValue
  /** Session name. */
  session: string
  /** Session lifetime. */
  sessionTtl: DurationValue
  /** Use FlareSolverr as a response-fallback path. */
  fallback: boolean
}

/* ---- 2e. Sources & Extensions --------------------------------------------- */

/** Which segment of the Sources & Extensions pane is showing. */
export type ExtensionTab = 'installed' | 'available' | 'repos'

/**
 * CachedVersion — one HELD (retained) .apk version of an extension the owner can
 * reinstall (roll back to) from Tsundoku's apk cache. Surfaced so the owner never
 * has to hunt through physical .apk files to undo a bad update.
 */
export interface CachedVersion {
  /** The numeric version code — the reinstall target. */
  versionCode: number
  /** The human-readable version string. */
  versionName: string
  /** ISO-8601 timestamp of when these bytes were cached. */
  cachedAt: string
}

/**
 * Extension — one Suwayomi source plugin. `id` is the Suwayomi `pkgName`
 * identity; `hasUpdate` drives the UPDATE badge on installed rows.
 */
export interface Extension {
  /** Suwayomi package name (identity + mutation target). */
  id: string
  /** Display name. */
  name: string
  /** Source language code (e.g. `en`, `zh`). */
  lang: string
  /** Installed/available version string. */
  version: string
  /** The installed/available numeric version code (marks the "current" held version). */
  versionCode: number
  /** Whether a newer version is available (installed rows only). */
  hasUpdate: boolean
  /**
   * Same-origin icon proxy path ("/api/suwayomi/extensions/{id}/icon").
   * ExtensionRow falls back to the tinted placeholder square on load error
   * (or when this is empty, e.g. a Storybook fixture with no backend).
   */
  iconUrl: string
  /**
   * The HELD (retained) versions of this extension in the apk cache — the
   * reversible-update history. Newest-first; [] when none held (e.g. an
   * available-but-not-installed extension). Each carries a "Reinstall this
   * version" action on installed rows.
   */
  cachedVersions: CachedVersion[]
}

/** Extension repository URL row (reorderable; one is the pre-populated default). */
export interface Repo {
  /** Stable row id (the remove/reorder target). */
  id: string
  /** The repo index URL. */
  url: string
  /** Whether this is the pre-populated default repo. */
  isDefault: boolean
}

/** The direction a reorder moves a row: up (−1) or down (+1) in the list. */
export type ReorderDirection = -1 | 1

/* ---- 2f. Source metrics (search performance) ------------------------------
 * The source-metric screen types (SourceMetric / SourceBreaker / SourceWarmth)
 * moved to `sourceHealth.types.ts` when the source-metrics UI relocated off the
 * Settings screen onto the `/health` Source Health tab (Source Health Console,
 * slice 3). Only the runtime CONFIG knobs (`SourcesSettings`, above) stay here. */

/* ---- 2g. Trackers (Phase 3d — connect + bind; sync is Phase 4) ------------ */

/**
 * TrackerStatus — one native tracker's (AniList, MAL, Kitsu, MangaUpdates)
 * connect status, screen-facing mirror of the backend `Tracker` DTO (no field
 * renames — it's already flat/camelCase). `needsOAuth` picks the row's shape:
 * true → AniList/MAL (a "Connect" full-tab redirect); false → Kitsu/MangaUpdates
 * (an inline username/password form). `isTokenExpired` is only ever true
 * alongside `isLoggedIn` (a disconnected tracker always reports it false).
 */
export interface TrackerStatus {
  /** Stable numeric registry id (MAL=1, AniList=2, Kitsu=3, MangaUpdates=7). */
  id: number
  /** Human display name. */
  name: string
  /** True for AniList/MAL (OAuth redirect); false for Kitsu/MangaUpdates (credentials). */
  needsOAuth: boolean
  /** Whether a connected account exists for this tracker. */
  isLoggedIn: boolean
  /** Whether the connected account's token has expired and needs a fresh login. */
  isTokenExpired: boolean
  /** The connected account's display username ("" when not logged in). */
  username: string
  /**
   * Whether this tracker's entries can be marked private on the remote
   * account (true for AniList/Kitsu; false for MAL/MangaUpdates, which have
   * no such remote concept — a bind `private` request field is silently
   * ignored for them). Drives the Trackers section's private eye-toggle.
   */
  supportsPrivate: boolean
}

/**
 * TrackerActionState — the §16 state of the one in-flight tracker action
 * (connect / OAuth or credential login / disconnect). Mirrors `RowActionState`
 * but keyed by the tracker's NUMERIC registry id rather than a string row id.
 */
export interface TrackerActionState {
  /** Id of the tracker currently mutating; null when idle. */
  busyId?: number | null
  /** A human-readable failure surfaced inline; empty/absent when none. */
  error?: string
}

/* ---- 2h. Network routing (per-source SOCKS + FlareSolverr) ----------------- */

/** Which field-group a network endpoint carries (drives the editor's form). */
export type NetworkEndpointKind = 'socks' | 'flaresolverr'

/**
 * FlareMode — a source's FlareSolverr routing scope: `none` (never solve),
 * `global` (the shared global FlareSolverr), or `endpoint` (a specific bound
 * FlareSolverr endpoint — pairs with a non-null `flareEndpointId`).
 */
export type FlareMode = 'none' | 'global' | 'endpoint'

/**
 * NetworkEndpoint — one named, reusable egress endpoint (Network pane). Screen
 * mirror of the backend `NetworkEndpoint` DTO. `kind` selects which field-group
 * is meaningful: the SOCKS group (host/port/socksVersion/username) for `socks`,
 * or the FlareSolverr group (url/session/sessionTtl/timeout/asResponseFallback)
 * for `flaresolverr`. The SOCKS password is WRITE-ONLY — the backend never returns
 * it, so this screen type carries no password field (the editor sends a new one
 * via `NetworkEndpointInput` only when the owner types it).
 */
export interface NetworkEndpoint {
  /** Endpoint UUID (the mutation target + row key). */
  id: string
  /** Owner-facing label. */
  name: string
  /** Which field-group applies. */
  kind: NetworkEndpointKind
  /** Whether a binding to this endpoint takes effect. */
  enabled: boolean
  /** SOCKS proxy host (kind=socks). */
  host: string
  /** SOCKS proxy port (kind=socks). */
  port: number
  /** SOCKS protocol version, 4 or 5 (kind=socks). */
  socksVersion: number
  /** Optional SOCKS auth username (kind=socks). */
  username: string
  /** FlareSolverr endpoint URL (kind=flaresolverr). */
  url: string
  /** FlareSolverr session identifier (kind=flaresolverr). */
  session: string
  /** FlareSolverr session TTL in minutes (kind=flaresolverr). */
  sessionTtl: number
  /** FlareSolverr per-request solve timeout in seconds (kind=flaresolverr). */
  timeout: number
  /** Use FlareSolverr only reactively, as a fallback for blocked requests (kind=flaresolverr). */
  asResponseFallback: boolean
}

/**
 * NetworkEndpointInput — the create/update payload the endpoint dialog emits.
 * `id` is null for a create and the endpoint id for an update. `password` is
 * WRITE-ONLY and optional: send it only when the owner typed a new one; leave it
 * blank/omitted to keep the stored password (update) or none (create).
 */
export interface NetworkEndpointInput {
  /** Null = create; an endpoint id = update. */
  id: string | null
  /** Owner-facing label. */
  name: string
  /** Which field-group the endpoint carries. */
  kind: NetworkEndpointKind
  /** Whether a binding to this endpoint takes effect. */
  enabled: boolean
  /** SOCKS proxy host (kind=socks). */
  host: string
  /** SOCKS proxy port (kind=socks). */
  port: number
  /** SOCKS protocol version, 4 or 5 (kind=socks). */
  socksVersion: number
  /** Optional SOCKS auth username (kind=socks). */
  username: string
  /** New SOCKS auth password (write-only); blank/omitted keeps the stored one. */
  password?: string
  /** FlareSolverr endpoint URL (kind=flaresolverr). */
  url: string
  /** FlareSolverr session identifier (kind=flaresolverr). */
  session: string
  /** FlareSolverr session TTL in minutes (kind=flaresolverr). */
  sessionTtl: number
  /** FlareSolverr per-request solve timeout in seconds (kind=flaresolverr). */
  timeout: number
  /** Use FlareSolverr only reactively, as a fallback for blocked requests (kind=flaresolverr). */
  asResponseFallback: boolean
}

/**
 * SourceBinding — one source's network-routing assignment (Network pane
 * assignment table). Screen mirror of the backend `SourceNetworkBinding` DTO;
 * the two nullable endpoint ids normalise absent → null (mapper parity with the
 * other composables). A source WITHOUT a binding is not represented here at all —
 * its absence means "use the global default".
 */
export interface SourceBinding {
  /** The engine-host source id (stringified decimal int64) — the row key. */
  sourceId: string
  /** The bound SOCKS endpoint, or null for the direct/global SOCKS default. */
  socksEndpointId: string | null
  /** FlareSolverr routing scope for the source. */
  flareMode: FlareMode
  /** The bound FlareSolverr endpoint; non-null iff flareMode=endpoint. */
  flareEndpointId: string | null
}

/**
 * NetworkSource — one engine source shown as a row in the assignment table
 * (`GET /api/sources`). Only the id/name/lang are needed to render + address a
 * binding row.
 */
export interface NetworkSource {
  /** Engine-host source id (stringified decimal int64) — the binding target. */
  id: string
  /** Human-readable source name. */
  name: string
  /** BCP-47 content language tag (e.g. "en", "ko"). */
  lang: string
}
