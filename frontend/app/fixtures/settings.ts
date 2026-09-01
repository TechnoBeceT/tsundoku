/**
 * Story-only fixtures for the Settings screen. NOT imported by app code — only
 * by Storybook stories — so the screen stays props-driven and backend-free.
 *
 * Mirrors the design prototype's seed state: the M12 library knobs, the
 * five seed categories (Other is the default landing), an embedded engine with an
 * upgrade available, the Tsundoku-owned FlareSolverr config (on, QCAT-238), and the
 * installed/available/repo extension sets.
 */
import type {
  DurationValue,
  EngineInfo,
  Extension,
  FlareSolverrConfig,
  ImpersonateConfig,
  LibrarySettings,
  NetworkEndpoint,
  Repo,
  SettingsCategory,
  SourcesSettings,
  SystemInfo,
  TrackerStatus,
  UpgradeStep,
} from '../components/screens/settings.types'
import type { SourceMetric } from '../components/screens/sourceHealth.types'
import type { components } from '../utils/api/schema.d.ts'

/** The runtime-editable library knobs (2a). */
export const librarySettings: LibrarySettings = {
  refreshInterval: { value: 2, unit: 'h' },
  downloadInterval: { value: 15, unit: 'm' },
  retryBackoff: { value: 10, unit: 'm' },
  maxRetries: 5,
  staleGraceDays: 14,
  refreshConcurrency: 4,
  downloadConcurrency: 5,
  maxConcurrentDownloads: 6,
}

/** Read-only deploy-time facts for the System card (2a). */
export const systemInfo: SystemInfo = {
  storageFolder: '/data/manga',
  serverPort: '9833',
  database: 'db:5432 / tsundoku',
}

/** The five seed categories — "Other" is the default landing. */
export const settingsCategories: SettingsCategory[] = [
  { id: 'cat-manga', name: 'Manga', count: 42, isDefault: false },
  { id: 'cat-manhwa', name: 'Manhwa', count: 28, isDefault: false },
  { id: 'cat-manhua', name: 'Manhua', count: 11, isDefault: false },
  { id: 'cat-comic', name: 'Comic', count: 0, isDefault: false },
  { id: 'cat-other', name: 'Other', count: 6, isDefault: true },
]

/** An embedded engine, running, with a newer pinned version available. */
export const engineInfo: EngineInfo = {
  mode: 'embedded',
  externalUrl: 'http://suwayomi:4567',
  runningVersion: 'v2.2.2100',
  pinnedVersion: 'v2.2.2200',
  runtimeDir: '/data/suwayomi',
  javaPath: 'java',
  status: 'running',
  upgradeAvailable: true,
  availableVersion: 'v2.2.2200',
}

/** A mid-flight upgrade stepper (Swap JAR active) — for the in-progress story. */
export const upgradeStepsInProgress: UpgradeStep[] = [
  { label: 'Clean stop', status: 'done' },
  { label: 'Backup', status: 'done' },
  { label: 'Swap JAR', status: 'active' },
  { label: 'Migration boot', status: 'pending' },
  { label: 'Verify', status: 'pending' },
]

/**
 * The Tsundoku-owned FlareSolverr config (QCAT-238) — served/saved through its
 * own endpoint in Download engine's Access & bypass section.
 */
export const flareSolverrConfig: FlareSolverrConfig = {
  enabled: true,
  url: 'http://flaresolverr:8191',
  timeout: { value: 60, unit: 's' },
  session: 'tsundoku',
  sessionTtl: { value: 15, unit: 'm' },
  fallback: true,
}

/**
 * The Tsundoku-owned impersonate-gateway config (GAP-111, scoped per source by
 * GAP-131) — the Chrome-fingerprint image proxy card that sits next to the
 * FlareSolverr card in Download engine's Access & bypass section. On, pointing at the
 * compose-network gateway, with ONE source opted in (the realistic shape: the
 * proxy exists for the rare source whose CDN blocks the default client).
 */
export const impersonateConfig: ImpersonateConfig = {
  enabled: true,
  url: 'http://impersonate-gateway:8788',
  sourceIds: ['1998416842837112832'],
}

/**
 * Installed extensions — two carry an available update (UPDATE badge). No
 * backend is running in Storybook, so iconUrl is '' here (the fallback tinted
 * square); ExtensionRow.stories.ts adds a dedicated icon fixture separately.
 */
export const installedExtensions: Extension[] = [
  { id: 'mangadex', name: 'MangaDex', lang: 'en', version: '1.4.21', versionCode: 42, hasUpdate: false, iconUrl: '', cachedVersions: [{ versionCode: 42, versionName: '1.4.21', cachedAt: '2026-07-10T00:00:00Z' }] },
  // Asura carries a rollback history: the current 1.4.9 plus two held older
  // builds the owner can reinstall (the reversible-update showcase).
  { id: 'asurascans', name: 'Asura Scans', lang: 'en', version: '1.4.9', versionCode: 49, hasUpdate: true, iconUrl: '', cachedVersions: [
    { versionCode: 49, versionName: '1.4.9', cachedAt: '2026-07-15T00:00:00Z' },
    { versionCode: 48, versionName: '1.4.8', cachedAt: '2026-06-28T00:00:00Z' },
    { versionCode: 47, versionName: '1.4.7', cachedAt: '2026-06-02T00:00:00Z' },
  ] },
  { id: 'comick', name: 'ComicK', lang: 'en', version: '2.0.3', versionCode: 203, hasUpdate: false, iconUrl: '', cachedVersions: [{ versionCode: 203, versionName: '2.0.3', cachedAt: '2026-07-12T00:00:00Z' }] },
  { id: 'weebcentral', name: 'Weeb Central', lang: 'en', version: '1.2.0', versionCode: 120, hasUpdate: true, iconUrl: '', cachedVersions: [{ versionCode: 120, versionName: '1.2.0', cachedAt: '2026-07-01T00:00:00Z' }] },
  { id: 'bilibili', name: 'BiliBili Comics', lang: 'zh', version: '1.3.7', versionCode: 137, hasUpdate: false, iconUrl: '', cachedVersions: [{ versionCode: 137, versionName: '1.3.7', cachedAt: '2026-07-08T00:00:00Z' }] },
]

/** Available (installable) extensions — nothing held (not installed). */
export const availableExtensions: Extension[] = [
  { id: 'reaperscans', name: 'Reaper Scans', lang: 'en', version: '1.5.1', versionCode: 151, hasUpdate: false, iconUrl: '', cachedVersions: [] },
  { id: 'flamecomics', name: 'Flame Comics', lang: 'en', version: '1.1.2', versionCode: 112, hasUpdate: false, iconUrl: '', cachedVersions: [] },
  { id: 'mangaplus', name: 'MANGA Plus', lang: 'en', version: '1.6.0', versionCode: 160, hasUpdate: false, iconUrl: '', cachedVersions: [] },
  { id: 'webtoons', name: 'Webtoons', lang: 'en', version: '2.1.0', versionCode: 210, hasUpdate: false, iconUrl: '', cachedVersions: [] },
  { id: 'kakao', name: 'Kakao', lang: 'ko', version: '1.0.4', versionCode: 104, hasUpdate: false, iconUrl: '', cachedVersions: [] },
]

/** Extension repositories — the first is the pre-populated default. */
export const repos: Repo[] = [
  { id: 'r1', url: 'https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.min.json', isDefault: true },
  { id: 'r2', url: 'https://raw.githubusercontent.com/my-org/tachi-extras/repo/index.min.json', isDefault: false },
]

/** Background extension update-check cadence (2e). */
export const extCheckInterval: DurationValue = { value: 12, unit: 'h' }

/** The 6 warm-up/politeness knobs (source-politeness spec), at their defaults. */
export const sourcesSettings: SourcesSettings = {
  warmupInterval: { value: 15, unit: 'm' },
  warmupSlowThresholdMs: 5000,
  failureThreshold: 5,
  cooldown: { value: 30, unit: 'm' },
  minRequestDelayMs: 500,
  imageRequestDelayMs: 500,
}

/** Warm-up disabled (0) — the "a source keeps getting IP-blocked" recommendation. */
export const sourcesSettingsWarmupDisabled: SourcesSettings = {
  ...sourcesSettings,
  warmupInterval: { value: 0, unit: 's' },
}

/* ---- Source configuration and exception states -------------------------- */

/** A healthy source using every global download-engine setting unchanged. */
export const fullyInheritedSourceConfiguration = {
  source: {
    sourceId: '2499283573021220255',
    name: 'MangaDex',
    language: 'en',
  },
  downloadConcurrency: { override: null, effective: 5, inherited: true },
  imageRequestDelay: { override: null, effective: '500ms', inherited: true },
  protection: {
    warmupInterval: '15m0s',
    warmupSlowThresholdMs: 5000,
    failureThreshold: 5,
    sourceCooldown: '30m0s',
    politenessDelay: '500ms',
  },
  bypassEnabled: true,
  reuseBypassSession: {
    override: null,
    global: false,
    effective: false,
    inherited: true,
    mode: 'disposable',
  },
  imageConnectionMode: {
    override: null,
    global: 'fresh',
    effective: 'fresh',
    inherited: true,
  },
  kcef: {
    override: null,
    global: 'auto',
    effective: 'auto',
    inherited: true,
    enabled: true,
  },
  imageProxy: {
    optedIn: false,
    gatewayEnabled: true,
    gatewayConfigured: true,
    effectiveAvailable: false,
  },
  routing: {
    stored: {
      configured: false,
      socksMode: 'global',
      socks: { endpointId: null, name: null },
      bypassMode: 'global',
      bypass: { endpointId: null, name: null },
    },
    socksMode: 'global',
    socks: { endpointId: null, name: null },
    bypassMode: 'global',
    bypass: { endpointId: null, name: null },
  },
  profileKey: 'default',
  runtime: {
    status: 'applied',
    desiredRevision: 12,
    appliedRevision: 12,
    lastApplyAttempt: '2026-08-30T14:10:00Z',
    lastApplyError: '',
  },
} satisfies components['schemas']['SourceEffectiveConfiguration']

/** Comic Asura tuned conservatively and routed through dedicated VPN services. */
export const comicAsuraSourceConfiguration = {
  source: {
    sourceId: '1024627298672457456',
    name: 'Comic Asura',
    language: 'en',
  },
  downloadConcurrency: { override: 1, effective: 1, inherited: false },
  imageRequestDelay: { override: '1250ms', effective: '1250ms', inherited: false },
  protection: {
    warmupInterval: '15m0s',
    warmupSlowThresholdMs: 5000,
    failureThreshold: 5,
    sourceCooldown: '30m0s',
    politenessDelay: '500ms',
  },
  bypassEnabled: true,
  reuseBypassSession: {
    override: false,
    global: true,
    effective: false,
    inherited: false,
    mode: 'disposable',
  },
  imageConnectionMode: {
    override: 'reuse',
    global: 'fresh',
    effective: 'reuse',
    inherited: false,
  },
  kcef: {
    override: 'disabled',
    global: 'auto',
    effective: 'disabled',
    inherited: false,
    enabled: false,
  },
  imageProxy: {
    optedIn: false,
    gatewayEnabled: true,
    gatewayConfigured: true,
    effectiveAvailable: false,
  },
  routing: {
    stored: {
      configured: true,
      socksMode: 'endpoint',
      socks: { endpointId: 'ep-vpn-socks', name: 'VPN SOCKS' },
      bypassMode: 'endpoint',
      bypass: { endpointId: 'ep-vpn-flare', name: 'VPN FlareSolverr' },
    },
    socksMode: 'endpoint',
    socks: { endpointId: 'ep-vpn-socks', name: 'VPN SOCKS' },
    bypassMode: 'endpoint',
    bypass: { endpointId: 'ep-vpn-flare', name: 'VPN FlareSolverr' },
  },
  profileKey: 'vpn-comic-asura',
  runtime: {
    status: 'applied',
    desiredRevision: 18,
    appliedRevision: 18,
    lastApplyAttempt: '2026-08-30T14:24:00Z',
    lastApplyError: '',
  },
} satisfies components['schemas']['SourceEffectiveConfiguration']

/** Hive Scans explicitly opted into the image proxy while transport stays inherited. */
export const hiveProxySourceConfiguration = {
  source: {
    sourceId: '1998416842837112832',
    name: 'Hive Scans',
    language: 'en',
  },
  downloadConcurrency: { override: null, effective: 5, inherited: true },
  imageRequestDelay: { override: null, effective: '500ms', inherited: true },
  protection: {
    warmupInterval: '15m0s',
    warmupSlowThresholdMs: 5000,
    failureThreshold: 5,
    sourceCooldown: '30m0s',
    politenessDelay: '500ms',
  },
  bypassEnabled: true,
  reuseBypassSession: {
    override: null,
    global: true,
    effective: true,
    inherited: true,
    mode: 'reusable',
  },
  imageConnectionMode: {
    override: null,
    global: 'fresh',
    effective: 'fresh',
    inherited: true,
  },
  kcef: {
    override: null,
    global: 'auto',
    effective: 'auto',
    inherited: true,
    enabled: true,
  },
  imageProxy: {
    optedIn: true,
    gatewayEnabled: true,
    gatewayConfigured: true,
    effectiveAvailable: true,
  },
  routing: {
    stored: {
      configured: false,
      socksMode: 'global',
      socks: { endpointId: null, name: null },
      bypassMode: 'global',
      bypass: { endpointId: null, name: null },
    },
    socksMode: 'global',
    socks: { endpointId: null, name: null },
    bypassMode: 'global',
    bypass: { endpointId: null, name: null },
  },
  profileKey: 'default',
  runtime: {
    status: 'applied',
    desiredRevision: 20,
    appliedRevision: 20,
    lastApplyAttempt: '2026-08-30T14:29:00Z',
    lastApplyError: '',
  },
} satisfies components['schemas']['SourceEffectiveConfiguration']

/** A source whose latest configuration revision is still converging. */
export const pendingSourceException = {
  source: comicAsuraSourceConfiguration.source,
  exceptionCount: 7,
  runtime: {
    status: 'pending',
    desiredRevision: 19,
    appliedRevision: 18,
    lastApplyAttempt: null,
    lastApplyError: '',
  },
} satisfies components['schemas']['SourceExceptionSummary']

/** A pending source that retains the latest failed runtime-apply diagnosis. */
export const errorSourceException = {
  source: {
    sourceId: '9127482910938471028',
    name: 'Comix',
    language: 'en',
  },
  exceptionCount: 2,
  runtime: {
    status: 'pending',
    desiredRevision: 27,
    appliedRevision: 26,
    lastApplyAttempt: '2026-08-30T14:32:00Z',
    lastApplyError: 'engine profile did not become healthy before the apply deadline',
  },
} satisfies components['schemas']['SourceExceptionSummary']

/** Empty exception-list response for an install with no source-specific settings. */
export const noSourceExceptions = [] satisfies components['schemas']['SourceExceptionSummary'][]

/** A deliberately long source label that exercises wrapping in compact settings rows. */
export const longNameSourceException = {
  source: {
    sourceId: '-9223372036854775808',
    name: 'The Extremely Long Source Name for Alternate English Releases and Archival Mirrors',
    language: 'en',
  },
  exceptionCount: 1,
  runtime: {
    status: 'applied',
    desiredRevision: 31,
    appliedRevision: 31,
    lastApplyAttempt: '2026-08-30T14:40:00Z',
    lastApplyError: '',
  },
} satisfies components['schemas']['SourceExceptionSummary']

/* ---- 2f. Source metrics --------------------------------------------------- */

// Warm/cold is derived from `lastWarmedAt` age against Date.now(), so the
// timestamps are computed relative to now: a "warm" row was warmed a few minutes
// ago (< the 15-min window), a "cold" one ~40 min ago. Story-only, so a live
// Date here is fine.
const now = Date.now()
const agoIso = (msAgo: number): string => new Date(now - msAgo).toISOString()
const inIso = (msAhead: number): string => new Date(now + msAhead).toISOString()
const MIN = 60_000

/**
 * A mix of source-performance snapshots (as the backend returns them, sorted
 * slowest-first by EWMA): a fast+warm source, a slow+erroring source whose
 * anti-ban breaker is TRIPPED (cooling down · retry ~28m — drives the Reset
 * flow), a never-warmed unmeasured source, and two healthy sources.
 */
export const sourceMetrics: SourceMetric[] = [
  {
    id: 'src-asura',
    name: 'Asura Scans',
    avgLatencyMs: 4200,
    lastLatencyMs: 5100,
    searchCount: 120,
    successCount: 70,
    failCount: 50,
    lastError: '',
    lastErrorAt: null,
    lastSuccessAt: agoIso(5 * MIN),
    lastWarmedAt: agoIso(5 * MIN),
    updatedAt: agoIso(1 * MIN),
    isSlow: true,
    breaker: null,
  },
  {
    id: 'src-comick',
    name: 'ComicK',
    avgLatencyMs: 1800,
    lastLatencyMs: 0,
    searchCount: 80,
    successCount: 40,
    failCount: 40,
    lastError: 'context deadline exceeded: FlareSolverr timed out after 60s while solving the Cloudflare challenge',
    lastErrorAt: agoIso(3 * MIN),
    lastSuccessAt: agoIso(50 * MIN),
    lastWarmedAt: agoIso(40 * MIN),
    updatedAt: agoIso(3 * MIN),
    isSlow: true,
    // Tripped anti-ban breaker — repeated Cloudflare timeouts pushed it into
    // cooldown; the row shows the "cooling down · retry ~28m" banner + Reset.
    breaker: {
      consecutiveFailures: 5,
      cooldownUntil: inIso(28 * MIN),
      lastError: 'context deadline exceeded: FlareSolverr timed out after 60s while solving the Cloudflare challenge',
      isCoolingDown: true,
    },
  },
  {
    id: 'src-weeb',
    name: 'Weeb Central',
    avgLatencyMs: 0,
    lastLatencyMs: 0,
    searchCount: 0,
    successCount: 0,
    failCount: 0,
    lastError: '',
    lastErrorAt: null,
    lastSuccessAt: null,
    lastWarmedAt: null,
    updatedAt: agoIso(2 * 60 * MIN),
    isSlow: true,
    breaker: null,
  },
  {
    id: 'src-bili',
    name: 'BiliBili Comics',
    avgLatencyMs: 600,
    lastLatencyMs: 620,
    searchCount: 40,
    successCount: 40,
    failCount: 0,
    lastError: '',
    lastErrorAt: null,
    lastSuccessAt: agoIso(46 * MIN),
    lastWarmedAt: agoIso(45 * MIN),
    updatedAt: agoIso(46 * MIN),
    isSlow: false,
    breaker: null,
  },
  {
    id: 'src-mangadex',
    name: 'MangaDex',
    avgLatencyMs: 240,
    lastLatencyMs: 210,
    searchCount: 500,
    successCount: 492,
    failCount: 8,
    lastError: '',
    lastErrorAt: null,
    lastSuccessAt: agoIso(2 * MIN),
    lastWarmedAt: agoIso(3 * MIN),
    updatedAt: agoIso(2 * MIN),
    isSlow: false,
    breaker: null,
  },
]

/**
 * The four registered trackers (2g) — one of each connect shape.
 * `supportsPrivate` mirrors the backend: true for AniList/Kitsu, false for
 * MAL/MangaUpdates (no remote private concept — see `TrackerStatus`).
 */
export const trackers: TrackerStatus[] = [
  { id: 2, name: 'AniList', needsOAuth: true, isLoggedIn: true, isTokenExpired: false, username: 'technobecet', supportsPrivate: true },
  { id: 1, name: 'MyAnimeList', needsOAuth: true, isLoggedIn: false, isTokenExpired: false, username: '', supportsPrivate: false },
  { id: 3, name: 'Kitsu', needsOAuth: false, isLoggedIn: false, isTokenExpired: false, username: '', supportsPrivate: true },
  { id: 7, name: 'MangaUpdates', needsOAuth: false, isLoggedIn: false, isTokenExpired: false, username: '', supportsPrivate: false },
]

/* ---- 2h. Network routing (per-source SOCKS + FlareSolverr) ----------------- */

/** Two reusable egress endpoints — a VPN SOCKS proxy + a VPN FlareSolverr. */
export const networkEndpoints: NetworkEndpoint[] = [
  {
    id: 'ep-vpn-socks',
    name: 'VPN SOCKS',
    kind: 'socks',
    enabled: true,
    host: '10.0.1.9',
    port: 1080,
    socksVersion: 5,
    username: 'tsundoku',
    url: '',
    session: '',
    sessionTtl: 0,
    timeout: 0,
    asResponseFallback: true,
  },
  {
    id: 'ep-vpn-flare',
    name: 'VPN FlareSolverr',
    kind: 'flaresolverr',
    enabled: true,
    host: '',
    port: 0,
    socksVersion: 5,
    username: '',
    url: 'http://flaresolverr-vpn:8191',
    session: 'sess-a',
    sessionTtl: 15,
    timeout: 60,
    asResponseFallback: false,
  },
]
