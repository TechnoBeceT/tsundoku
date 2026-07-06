/**
 * useSettings — data layer for the Settings → Library pane + standalone tunables.
 *
 * Fetches GET /api/settings and GET /api/system in parallel; maps the backend
 * Setting[] and System DTO onto LibrarySettings, SystemInfo, and the standalone
 * extensionCheckInterval tunable. Exposes saveLibrary() and saveExtensionCheckInterval()
 * with the §16 SaveState lifecycle: idle → saving → success/error.
 *
 * Duration helpers (exported for testability):
 *   parseGoDuration("2h0m0s") → { value: 2, unit: 'h' }
 *   parseGoDuration("90m0s")  → { value: 90, unit: 'm' }
 *   formatGoDuration({ value: 30, unit: 's' }) → "30s"
 *
 * The parse → format round-trip is stable: the serialised form ("2h", "90m", "30s")
 * is accepted by Go's time.ParseDuration without modification.
 *
 * Key mapping (backend key → LibrarySettings field):
 *   jobs.refresh_interval         → refreshInterval  (DurationValue)
 *   jobs.download_interval        → downloadInterval (DurationValue)
 *   jobs.retry_backoff            → retryBackoff     (DurationValue)
 *   jobs.max_retries              → maxRetries       (number)
 *   health.stale_grace_days       → staleGraceDays   (number)
 *   jobs.refresh_concurrency      → refreshConcurrency (number)
 *   jobs.download_concurrency     → downloadConcurrency (number)
 *
 * Standalone tunables (not part of LibrarySettings — live in other panes):
 *   jobs.extension_check_interval → extensionCheckInterval (DurationValue)
 *
 * Sources pane tunables (SourcesSettings — the source-politeness spec):
 *   jobs.warmup_interval           → warmupInterval        (DurationValue)
 *   jobs.warmup_slow_threshold_ms  → warmupSlowThresholdMs (number, ms — a
 *                                    plain int like maxRetries/staleGraceDays)
 *   sources.failure_threshold      → failureThreshold      (number)
 *   sources.cooldown               → cooldown              (DurationValue)
 *   sources.min_request_delay      → minRequestDelayMs     (number, ms — a
 *                                    true backend DURATION, but edited/
 *                                    serialised at MILLISECOND granularity
 *                                    since its default (500ms) and 0=off floor
 *                                    are sub-minute; see parseGoDurationMs /
 *                                    formatMsDuration below, NOT parseGoDuration
 *                                    (that h/m/s parser misreads "500ms" as
 *                                    500 minutes — the "m" in "ms" collides).
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { DurationValue, LibrarySettings, SourcesSettings, SystemInfo, SaveState } from '~/components/screens/settings.types'

type SettingDTO = components['schemas']['Setting']
type SystemDTO = components['schemas']['System']

// ── Duration helpers ──────────────────────────────────────────────────────────

/**
 * Parse a Go duration string to a DurationValue using the largest whole unit.
 *
 * Go always emits multi-component strings (e.g. "2h0m0s"), but also accepts
 * bare single-component strings on input ("2h", "90m", "30s") — both forms are
 * handled here. The selection rule: try hours (3600 s), then minutes (60 s),
 * then seconds; pick the first that divides the total seconds evenly.
 *
 * Edge case: totalSeconds = 0 → { value: 0, unit: 's' }.
 */
export function parseGoDuration(s: string): DurationValue {
  const h = /(\d+)h/.exec(s)?.[1]
  const m = /(\d+)m/.exec(s)?.[1]
  const sec = /(\d+)s/.exec(s)?.[1]

  const totalSeconds =
    (h !== undefined ? Number(h) * 3600 : 0) +
    (m !== undefined ? Number(m) * 60 : 0) +
    (sec !== undefined ? Number(sec) : 0)

  if (totalSeconds > 0 && totalSeconds % 3600 === 0) return { value: totalSeconds / 3600, unit: 'h' }
  if (totalSeconds > 0 && totalSeconds % 60 === 0) return { value: totalSeconds / 60, unit: 'm' }
  return { value: totalSeconds, unit: 's' }
}

/**
 * Serialise a DurationValue to a Go-parseable duration string.
 * Go's time.ParseDuration accepts bare "2h", "90m", "30s" — no trailing zeroes needed.
 */
export function formatGoDuration(d: DurationValue): string {
  return `${d.value}${d.unit}`
}

// Matches one Go-duration component: an integer or decimal amount followed by
// its unit suffix. "ms" is listed before "m" in the alternation so "500ms"
// parses as 500 milliseconds, not 500 minutes ("m" is a prefix of "ms").
const DURATION_COMPONENT_RE = /(\d+(?:\.\d+)?)(ms|h|m|s)/g

/**
 * Parse a Go duration string to its total whole milliseconds — used for
 * `sources.min_request_delay`, a true backend duration whose default (500ms)
 * and 0=off floor need millisecond, not h/m/s, granularity. Unlike
 * `parseGoDuration` (which only recognises bare h/m/s and would misread the
 * "m" inside "500ms" as minutes), this walks every component ("2h", "1m",
 * "30.5s", "500ms", …) and sums them, so it correctly handles whatever
 * canonical form `time.Duration.String()` returns (including fractional
 * seconds, e.g. "2.5s" for a 2500ms delay).
 *
 * Edge case: an unparseable/empty string → 0.
 */
export function parseGoDurationMs(raw: string): number {
  let totalMs = 0
  for (const match of raw.matchAll(DURATION_COMPONENT_RE)) {
    const amount = Number.parseFloat(match[1] ?? '0')
    switch (match[2]) {
      case 'h': totalMs += amount * 3_600_000; break
      case 'm': totalMs += amount * 60_000; break
      case 's': totalMs += amount * 1_000; break
      case 'ms': totalMs += amount; break
    }
  }
  return Math.round(totalMs)
}

/**
 * Serialise a millisecond amount to a Go-parseable duration string, e.g.
 * `formatMsDuration(500)` → `"500ms"`. Always emits the `ms` unit — simplest
 * canonical form `time.ParseDuration` accepts, round-trips exactly for the
 * millisecond-granularity `sources.min_request_delay` knob.
 */
export function formatMsDuration(ms: number): string {
  return `${Math.max(0, Math.round(ms))}ms`
}

// ── Default fallbacks (used when the backend omits a key — should not happen) ─

const DEFAULTS: LibrarySettings = {
  refreshInterval: { value: 2, unit: 'h' },
  downloadInterval: { value: 15, unit: 'm' },
  retryBackoff: { value: 1, unit: 'm' },
  maxRetries: 3,
  staleGraceDays: 14,
  refreshConcurrency: 4,
  downloadConcurrency: 5,
}

// Default for the standalone extension-check-interval tunable (Extensions pane).
const EXT_CHECK_DEFAULT: DurationValue = { value: 24, unit: 'h' }

// Defaults for the Sources pane tunables (source-politeness spec; mirrors the
// backend config defaults in internal/settings/tunables.go).
const SOURCES_DEFAULTS: SourcesSettings = {
  warmupInterval: { value: 15, unit: 'm' },
  warmupSlowThresholdMs: 5000,
  failureThreshold: 5,
  cooldown: { value: 30, unit: 'm' },
  minRequestDelayMs: 500,
}

// ── DTO mappers ───────────────────────────────────────────────────────────────

function mapSettings(settings: SettingDTO[]): LibrarySettings {
  const v = (key: string): string | undefined => settings.find(s => s.key === key)?.value

  const dur = (key: string, fallback: DurationValue): DurationValue => {
    const raw = v(key)
    return raw !== undefined ? parseGoDuration(raw) : { ...fallback }
  }

  const int = (key: string, fallback: number): number => {
    const raw = v(key)
    return raw !== undefined ? Number(raw) : fallback
  }

  return {
    refreshInterval: dur('jobs.refresh_interval', DEFAULTS.refreshInterval),
    downloadInterval: dur('jobs.download_interval', DEFAULTS.downloadInterval),
    retryBackoff: dur('jobs.retry_backoff', DEFAULTS.retryBackoff),
    maxRetries: int('jobs.max_retries', DEFAULTS.maxRetries),
    staleGraceDays: int('health.stale_grace_days', DEFAULTS.staleGraceDays),
    refreshConcurrency: int('jobs.refresh_concurrency', DEFAULTS.refreshConcurrency),
    downloadConcurrency: int('jobs.download_concurrency', DEFAULTS.downloadConcurrency),
  }
}

function mapSystem(dto: SystemDTO): SystemInfo {
  return {
    storageFolder: dto.storageFolder,
    serverPort: dto.serverPort,
    database: dto.database,
  }
}

/** Maps the Sources pane's 5 keys from the settings list (see the key-mapping doc above). */
function mapSourcesSettings(settings: SettingDTO[]): SourcesSettings {
  const v = (key: string): string | undefined => settings.find(s => s.key === key)?.value

  const dur = (key: string, fallback: DurationValue): DurationValue => {
    const raw = v(key)
    return raw !== undefined ? parseGoDuration(raw) : { ...fallback }
  }

  const int = (key: string, fallback: number): number => {
    const raw = v(key)
    return raw !== undefined ? Number(raw) : fallback
  }

  const ms = (key: string, fallback: number): number => {
    const raw = v(key)
    return raw !== undefined ? parseGoDurationMs(raw) : fallback
  }

  return {
    warmupInterval: dur('jobs.warmup_interval', SOURCES_DEFAULTS.warmupInterval),
    warmupSlowThresholdMs: int('jobs.warmup_slow_threshold_ms', SOURCES_DEFAULTS.warmupSlowThresholdMs),
    failureThreshold: int('sources.failure_threshold', SOURCES_DEFAULTS.failureThreshold),
    cooldown: dur('sources.cooldown', SOURCES_DEFAULTS.cooldown),
    minRequestDelayMs: ms('sources.min_request_delay', SOURCES_DEFAULTS.minRequestDelayMs),
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useSettings() {
  const library = ref<LibrarySettings>({
    refreshInterval: { ...DEFAULTS.refreshInterval },
    downloadInterval: { ...DEFAULTS.downloadInterval },
    retryBackoff: { ...DEFAULTS.retryBackoff },
    maxRetries: DEFAULTS.maxRetries,
    staleGraceDays: DEFAULTS.staleGraceDays,
    refreshConcurrency: DEFAULTS.refreshConcurrency,
    downloadConcurrency: DEFAULTS.downloadConcurrency,
  })
  const system = ref<SystemInfo>({ storageFolder: '', serverPort: '', database: '' })
  const librarySave = ref<SaveState>({ status: 'idle' })

  // Standalone tunable: extension-check cadence (Extensions pane, not LibrarySettings).
  const extensionCheckInterval = ref<DurationValue>({ ...EXT_CHECK_DEFAULT })
  const extSave = ref<SaveState>({ status: 'idle' })

  // Sources pane: warm-up + circuit-breaker + politeness-delay tunables.
  const sourcesSettings = ref<SourcesSettings>({
    warmupInterval: { ...SOURCES_DEFAULTS.warmupInterval },
    warmupSlowThresholdMs: SOURCES_DEFAULTS.warmupSlowThresholdMs,
    failureThreshold: SOURCES_DEFAULTS.failureThreshold,
    cooldown: { ...SOURCES_DEFAULTS.cooldown },
    minRequestDelayMs: SOURCES_DEFAULTS.minRequestDelayMs,
  })
  const sourcesSettingsSave = ref<SaveState>({ status: 'idle' })

  const pending = ref(false)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [settingsRes, systemRes] = await Promise.all([
        apiClient.GET('/api/settings'),
        apiClient.GET('/api/system'),
      ])
      if (settingsRes.error || !settingsRes.data) throw new Error('Failed to load settings')
      if (systemRes.error || !systemRes.data) throw new Error('Failed to load system info')
      library.value = mapSettings(settingsRes.data)
      system.value = mapSystem(systemRes.data)
      // Standalone: extract extension-check interval from the same settings list.
      const rawExtCheck = settingsRes.data.find(s => s.key === 'jobs.extension_check_interval')?.value
      extensionCheckInterval.value = rawExtCheck !== undefined
        ? parseGoDuration(rawExtCheck)
        : { ...EXT_CHECK_DEFAULT }
      // Sources pane: the same settings list carries all 5 warm-up/politeness keys.
      sourcesSettings.value = mapSourcesSettings(settingsRes.data)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load settings'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * §16 save: build the key/value batch from the edited LibrarySettings, PATCH
   * /api/settings, drive librarySave through the SaveState lifecycle, and reseed
   * library from the authoritative response (never the local copy).
   *
   * The backend returns the full updated list on success; on a validation error
   * it returns { message } naming the bad key — surfaced verbatim as the error
   * message so the UI can display it inline.
   */
  async function saveLibrary(next: LibrarySettings): Promise<void> {
    librarySave.value = { status: 'saving' }
    try {
      const res = await apiClient.PATCH('/api/settings', {
        body: {
          settings: [
            { key: 'jobs.refresh_interval', value: formatGoDuration(next.refreshInterval) },
            { key: 'jobs.download_interval', value: formatGoDuration(next.downloadInterval) },
            { key: 'jobs.retry_backoff', value: formatGoDuration(next.retryBackoff) },
            { key: 'jobs.max_retries', value: String(next.maxRetries) },
            { key: 'health.stale_grace_days', value: String(next.staleGraceDays) },
            { key: 'jobs.refresh_concurrency', value: String(next.refreshConcurrency) },
            { key: 'jobs.download_concurrency', value: String(next.downloadConcurrency) },
          ],
        },
      })
      if (res.error) {
        // openapi-fetch parses the error body; the backend emits { message: "..." }.
        const msg = (res.error as { message?: string }).message ?? 'Save failed'
        librarySave.value = { status: 'error', message: msg }
        return
      }
      // §16: use the response body (authoritative server state), not the local copy.
      if (res.data) library.value = mapSettings(res.data)
      librarySave.value = { status: 'success' }
    }
    catch (err) {
      const msg = err instanceof Error ? err.message : 'Save failed'
      librarySave.value = { status: 'error', message: msg }
    }
  }

  /**
   * §16 save for the standalone extension-check-interval tunable. Sends a
   * single-key PATCH, drives extSave through the SaveState lifecycle, and
   * reseeds extensionCheckInterval from the authoritative response.
   */
  async function saveExtensionCheckInterval(d: DurationValue): Promise<void> {
    extSave.value = { status: 'saving' }
    try {
      const res = await apiClient.PATCH('/api/settings', {
        body: {
          settings: [{ key: 'jobs.extension_check_interval', value: formatGoDuration(d) }],
        },
      })
      if (res.error) {
        const msg = (res.error as { message?: string }).message ?? 'Save failed'
        extSave.value = { status: 'error', message: msg }
        return
      }
      // §16: reseed from the authoritative response body.
      if (res.data) {
        const raw = res.data.find(s => s.key === 'jobs.extension_check_interval')?.value
        extensionCheckInterval.value = raw !== undefined ? parseGoDuration(raw) : { ...EXT_CHECK_DEFAULT }
      }
      extSave.value = { status: 'success' }
    }
    catch (err) {
      const msg = err instanceof Error ? err.message : 'Save failed'
      extSave.value = { status: 'error', message: msg }
    }
  }

  /**
   * §16 save for the Sources pane: builds the 5-key batch from the edited
   * SourcesSettings, PATCHes /api/settings, drives sourcesSettingsSave through
   * the SaveState lifecycle, and reseeds sourcesSettings from the authoritative
   * response (never the local copy) — mirrors saveLibrary.
   */
  async function saveSourcesSettings(next: SourcesSettings): Promise<void> {
    sourcesSettingsSave.value = { status: 'saving' }
    try {
      const res = await apiClient.PATCH('/api/settings', {
        body: {
          settings: [
            { key: 'jobs.warmup_interval', value: formatGoDuration(next.warmupInterval) },
            { key: 'jobs.warmup_slow_threshold_ms', value: String(next.warmupSlowThresholdMs) },
            { key: 'sources.failure_threshold', value: String(next.failureThreshold) },
            { key: 'sources.cooldown', value: formatGoDuration(next.cooldown) },
            { key: 'sources.min_request_delay', value: formatMsDuration(next.minRequestDelayMs) },
          ],
        },
      })
      if (res.error) {
        const msg = (res.error as { message?: string }).message ?? 'Save failed'
        sourcesSettingsSave.value = { status: 'error', message: msg }
        return
      }
      // §16: reseed from the authoritative response body, not the local copy.
      if (res.data) sourcesSettings.value = mapSourcesSettings(res.data)
      sourcesSettingsSave.value = { status: 'success' }
    }
    catch (err) {
      const msg = err instanceof Error ? err.message : 'Save failed'
      sourcesSettingsSave.value = { status: 'error', message: msg }
    }
  }

  void refresh()

  return {
    library,
    system,
    librarySave,
    extensionCheckInterval,
    extSave,
    sourcesSettings,
    sourcesSettingsSave,
    pending,
    error,
    saveLibrary,
    saveExtensionCheckInterval,
    saveSourcesSettings,
    refresh,
  }
}
