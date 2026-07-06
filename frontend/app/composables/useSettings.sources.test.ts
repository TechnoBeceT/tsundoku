/**
 * useSettings – Sources pane tunables (source-politeness spec): the warm-up
 * cadence/threshold + the per-source circuit-breaker/politeness-delay knobs.
 *
 * Mirrors useSettings.downloadConcurrency.test.ts's structure, but pins all
 * 5 Sources-pane keys, including the millisecond-granularity duration
 * (`sources.min_request_delay`, formatted "500ms" not "0.5s") and the plain
 * ms-int (`jobs.warmup_slow_threshold_ms`).
 *
 * Non-vacuous:
 *   - If any of the 5 keys were missing from the load map, the corresponding
 *     field would silently keep its SOURCES_DEFAULTS fallback instead of the
 *     fixture's distinct value — each assertion below pins a fixture value
 *     that differs from the default, so a dropped mapping fails it.
 *   - If `saveSourcesSettings` omitted a key from its PATCH batch, or formatted
 *     `minRequestDelayMs` through the h/m/s `formatGoDuration` instead of
 *     `formatMsDuration`, the `patchBody` assertion (full 5-key array) fails.
 *   - `parseGoDurationMs` is pinned directly against the "m"-inside-"ms"
 *     collision that would misread "500ms" as 500 minutes if the h/m/s-only
 *     `parseGoDuration` were reused for this key.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { apiClient } from '~/utils/api/client'
import { useSettings, parseGoDurationMs, formatMsDuration } from './useSettings'

// ── Settings response fixtures ────────────────────────────────────────────────

const BASE_SETTINGS = [
  { key: 'jobs.refresh_interval', value: '2h0m0s' },
  { key: 'jobs.download_interval', value: '15m0s' },
  { key: 'jobs.retry_backoff', value: '1m0s' },
  { key: 'jobs.max_retries', value: '3' },
  { key: 'health.stale_grace_days', value: '14' },
  { key: 'jobs.refresh_concurrency', value: '4' },
  { key: 'jobs.download_concurrency', value: '5' },
  { key: 'jobs.warmup_interval', value: '20m0s' },
  { key: 'jobs.warmup_slow_threshold_ms', value: '6000' },
  { key: 'sources.failure_threshold', value: '7' },
  { key: 'sources.cooldown', value: '45m0s' },
  { key: 'sources.min_request_delay', value: '500ms' },
]

const SYSTEM_RESPONSE = {
  storageFolder: '/data/manga',
  serverPort: '4567',
  database: 'postgres@localhost/tsundoku',
}

// ── Call tracking ─────────────────────────────────────────────────────────────

// Captured PATCH body — reassigned per call so beforeEach can reset it.
let patchBody: unknown = null

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/settings') return Promise.resolve({ data: BASE_SETTINGS, error: null })
      if (path === '/api/system') return Promise.resolve({ data: SYSTEM_RESPONSE, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation((_path: string, opts: { body: unknown }) => {
      patchBody = opts.body
      // §16: return the authoritative updated list so the composable can reseed.
      return Promise.resolve({ data: BASE_SETTINGS, error: null })
    }),
    POST: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useSettings – sourcesSettings', () => {
  beforeEach(() => {
    patchBody = null
  })

  it('maps all 5 keys onto sourcesSettings', async () => {
    const { sourcesSettings } = useSettings()

    await vi.waitFor(() => {
      expect(sourcesSettings.value).toEqual({
        warmupInterval: { value: 20, unit: 'm' },
        warmupSlowThresholdMs: 6000,
        failureThreshold: 7,
        cooldown: { value: 45, unit: 'm' },
        minRequestDelayMs: 500,
      })
    })
  })

  it('saveSourcesSettings PATCHes all 5 keys in the backend format and drives idle→saving→success', async () => {
    const { sourcesSettings, saveSourcesSettings, sourcesSettingsSave } = useSettings()

    // Wait for the initial load so the edited copy starts from live values.
    await vi.waitFor(() => {
      expect(sourcesSettings.value.failureThreshold).toBe(7)
    })

    expect(sourcesSettingsSave.value.status).toBe('idle')

    await saveSourcesSettings({
      warmupInterval: { value: 0, unit: 's' },
      warmupSlowThresholdMs: 8000,
      failureThreshold: 5,
      cooldown: { value: 30, unit: 'm' },
      minRequestDelayMs: 500,
    })

    expect(patchBody).toEqual({
      settings: [
        { key: 'jobs.warmup_interval', value: '0s' },
        { key: 'jobs.warmup_slow_threshold_ms', value: '8000' },
        { key: 'sources.failure_threshold', value: '5' },
        { key: 'sources.cooldown', value: '30m' },
        { key: 'sources.min_request_delay', value: '500ms' },
      ],
    })

    expect(sourcesSettingsSave.value.status).toBe('success')
    // §16: reseeds from the authoritative response, not the local copy — the
    // mocked PATCH returns BASE_SETTINGS (failureThreshold stays 7).
    expect(sourcesSettings.value.failureThreshold).toBe(7)
  })

  it('surfaces a PATCH error naming the bad key instead of silently succeeding', async () => {
    vi.mocked(apiClient.PATCH).mockImplementationOnce(() =>
      Promise.resolve({ error: { message: 'sources.failure_threshold must be in [1, 100] (got 0)' }, response: new Response(null, { status: 400 }) }),
    )

    const { sourcesSettings, saveSourcesSettings, sourcesSettingsSave } = useSettings()
    await vi.waitFor(() => {
      expect(sourcesSettings.value.failureThreshold).toBe(7)
    })

    await saveSourcesSettings({ ...sourcesSettings.value, failureThreshold: 0 })

    expect(sourcesSettingsSave.value).toEqual({
      status: 'error',
      message: 'sources.failure_threshold must be in [1, 100] (got 0)',
    })
  })
})

// ── parseGoDurationMs / formatMsDuration (pure helpers) ───────────────────────

describe('parseGoDurationMs', () => {
  it('parses a bare "ms" component without mistaking it for minutes', () => {
    // The regression this guards: an h/m/s-only parser reads "500ms" as
    // "500m" (500 minutes) because "m" is a prefix of "ms".
    expect(parseGoDurationMs('500ms')).toBe(500)
  })

  it('parses "0s" (the disabled canonical form) as 0', () => {
    expect(parseGoDurationMs('0s')).toBe(0)
  })

  it('sums multi-component durations, including a fractional smallest unit', () => {
    expect(parseGoDurationMs('1m30.5s')).toBe(90_500)
    expect(parseGoDurationMs('2h0m0s')).toBe(7_200_000)
  })

  it('parses a fractional-second form Go emits for a non-round-ms delay', () => {
    // 2500ms canonicalises via time.Duration.String() to "2.5s".
    expect(parseGoDurationMs('2.5s')).toBe(2500)
  })
})

describe('formatMsDuration', () => {
  it('formats whole milliseconds with the "ms" suffix', () => {
    expect(formatMsDuration(500)).toBe('500ms')
  })

  it('clamps a negative value to 0', () => {
    expect(formatMsDuration(-5)).toBe('0ms')
  })

  it('round-trips through parseGoDurationMs', () => {
    expect(parseGoDurationMs(formatMsDuration(1234))).toBe(1234)
  })
})
