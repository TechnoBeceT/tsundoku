/**
 * Prop/data types for the Settings screen — the single-owner control panel.
 *
 * These mirror the PLANNED Settings surface (see the Settings design brief):
 * a few runtime-editable Tsundoku knobs (the M12 allowlist), user-defined
 * categories, read-only engine info + the embedded-engine upgrade flow, the
 * proxied Suwayomi server config (SOCKS + FlareSolverr), and extension/repo
 * management. Everything is presentation-only: the screen receives state via
 * props and emits every mutation — no fetching, routing, or stores. Kept in
 * this `.ts` (never exported from a `.vue`) so stories + fixtures import freely.
 */

/** The five settings panes, selected from the sticky sidebar nav. */
export type SettingsPane = 'library' | 'categories' | 'engine' | 'suwayomi' | 'extensions'

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

/* ---- 2d. Suwayomi server config (proxied) --------------------------------- */

/** Read-only display of the engine's DB backend (a deploy concern). */
export interface SuwayomiDatabaseInfo {
  /** DB engine type, e.g. `PostgreSQL`. */
  type: string
  /** JDBC connection URL. */
  url: string
  /** DB username (the password is never exposed). */
  username: string
}

/** Editable SOCKS-proxy config, gated behind the enable toggle. */
export interface SocksProxyConfig {
  /** Route source traffic through a SOCKS proxy when true. */
  enabled: boolean
  /** SOCKS version string (`4` or `5`). */
  version: string
  /** Proxy host. */
  host: string
  /** Proxy port (kept as a string — Suwayomi types it as `String!`). */
  port: string
  /** Proxy username. */
  username: string
  /** Proxy password (rendered masked). */
  password: string
}

/** Editable FlareSolverr (Cloudflare-bypass) config, gated behind the toggle. */
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

/** The whole proxied Suwayomi server config (read-only DB + two editable cards). */
export interface SuwayomiConfig {
  /** Read-only DB backend display. */
  database: SuwayomiDatabaseInfo
  /** Editable SOCKS proxy. */
  socks: SocksProxyConfig
  /** Editable FlareSolverr. */
  flareSolverr: FlareSolverrConfig
}

/* ---- 2e. Sources & Extensions --------------------------------------------- */

/** Which segment of the Sources & Extensions pane is showing. */
export type ExtensionTab = 'installed' | 'available' | 'repos'

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
  /** Whether a newer version is available (installed rows only). */
  hasUpdate: boolean
  /**
   * Same-origin icon proxy path ("/api/suwayomi/extensions/{id}/icon").
   * ExtensionRow falls back to the tinted placeholder square on load error
   * (or when this is empty, e.g. a Storybook fixture with no backend).
   */
  iconUrl: string
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
