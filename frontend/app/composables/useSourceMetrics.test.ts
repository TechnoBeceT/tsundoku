/**
 * useSourceMetrics – fetch mapping, warm-now round-trip, empty + error paths.
 *
 * Pins four behaviours:
 *   1. The initial load maps the SourceMetric DTO → screen SourceMetric with the
 *      RENAMES (sourceId→id, sourceName→name, ewmaLatencyMs→avgLatencyMs) and the
 *      undefined→null timestamp normalisation.
 *   2. warmNow() POSTs /api/sources/warmup (which now returns 202 {started:true})
 *      and surfaces the "started" message (the pass runs in the background — no
 *      count, and no synchronous refetch; the delayed refetch is timer-driven and
 *      intentionally NOT coupled to this test).
 *   3. An empty list ([]) is handled gracefully — metrics stays [].
 *   4. A failed load surfaces in `error`; a failed warm-up in `warmError`.
 *   5. The breaker DTO maps onto the screen breaker object (absent → null).
 *   6. resetBreaker() POSTs the reset endpoint then refetches; a failure lands in
 *      `resetError` verbatim.
 *
 * Non-vacuous: if the mapper dropped the rename, assertion 1 (id === 'src-1',
 * avgLatencyMs === 320) would fail on undefined; if warmNow did NOT post,
 * postCount would stay at 0 and assertion 2 would fail; if the error path
 * swallowed the failure, `error`/`warmError` would stay null.
 *
 * Note the asymmetry (mirrors useExtensions/useSettings): a failed GET surfaces a
 * generic load message, while a failed warm-up POST surfaces the server's own
 * error message verbatim.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceMetrics } from './useSourceMetrics'

// ── Fixtures ──────────────────────────────────────────────────────────────────

const RAW_METRICS = [
  {
    sourceId: 'src-1',
    sourceName: 'MangaDex',
    ewmaLatencyMs: 320,
    lastLatencyMs: 280,
    searchCount: 200,
    successCount: 196,
    failCount: 4,
    lastError: '',
    // lastErrorAt intentionally absent → must normalise to null.
    lastSuccessAt: '2026-07-05T10:00:00Z',
    lastWarmedAt: '2026-07-05T09:58:00Z',
    updatedAt: '2026-07-05T10:00:00Z',
    isSlow: false,
  },
  {
    sourceId: 'src-2',
    sourceName: 'Asura Scans',
    ewmaLatencyMs: 4200,
    lastLatencyMs: 5100,
    searchCount: 120,
    successCount: 70,
    failCount: 50,
    lastError: 'context deadline exceeded',
    lastErrorAt: '2026-07-05T09:55:00Z',
    lastSuccessAt: '2026-07-05T09:40:00Z',
    // lastWarmedAt intentionally absent → must normalise to null.
    updatedAt: '2026-07-05T10:00:00Z',
    isSlow: true,
    // A tripped anti-ban breaker → must map onto the screen breaker object.
    breaker: {
      consecutiveFailures: 5,
      cooldownUntil: '2026-07-05T10:30:00Z',
      lastError: 'context deadline exceeded',
      isCoolingDown: true,
    },
  },
]

// ── Call tracking + toggles ─────────────────────────────────────────────────

let getCount = 0
let postCount = 0
let resetCount = 0
let getError = false
let postError = false
let resetError = false
let emptyMetrics = false

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/sources/metrics') {
        getCount++
        if (getError) return Promise.resolve({ data: null, error: { message: 'boom' } })
        return Promise.resolve({ data: emptyMetrics ? [] : RAW_METRICS, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/sources/warmup') {
        postCount++
        if (postError) return Promise.resolve({ data: null, error: { message: 'warm failed' } })
        return Promise.resolve({ data: { started: true }, error: null })
      }
      if (path === '/api/sources/{sourceId}/reset-breaker') {
        resetCount++
        if (resetError) return Promise.resolve({ data: null, error: { message: 'reset failed' } })
        return Promise.resolve({ data: { sourceId: 'src-2', sourceName: 'Asura Scans' }, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useSourceMetrics', () => {
  beforeEach(() => {
    getCount = 0
    postCount = 0
    resetCount = 0
    getError = false
    postError = false
    resetError = false
    emptyMetrics = false
  })

  it('maps the SourceMetric DTO → screen SourceMetric with renames + null normalisation', async () => {
    const { metrics, pending } = useSourceMetrics()

    await vi.waitFor(() => expect(pending.value).toBe(false))

    expect(metrics.value).toHaveLength(2)
    const first = metrics.value[0]!
    expect(first.id).toBe('src-1')
    expect(first.name).toBe('MangaDex')
    expect(first.avgLatencyMs).toBe(320)
    expect(first.lastLatencyMs).toBe(280)
    expect(first.successCount).toBe(196)
    // Absent lastErrorAt → null; present lastWarmedAt passes through.
    expect(first.lastErrorAt).toBeNull()
    expect(first.lastWarmedAt).toBe('2026-07-05T09:58:00Z')

    // Absent lastWarmedAt on the second row → null.
    expect(metrics.value[1]!.lastWarmedAt).toBeNull()
    expect(metrics.value[1]!.isSlow).toBe(true)

    // The breaker maps onto the screen object; a source with no breaker → null.
    expect(first.breaker).toBeNull()
    expect(metrics.value[1]!.breaker).toEqual({
      consecutiveFailures: 5,
      cooldownUntil: '2026-07-05T10:30:00Z',
      lastError: 'context deadline exceeded',
      isCoolingDown: true,
    })
  })

  it('resetBreaker() POSTs the reset endpoint, then refetches to clear the state', async () => {
    const { pending, resetBreaker, resetting, resetError: resetErr } = useSourceMetrics()

    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(getCount).toBe(1)

    await resetBreaker('src-2')

    // Exactly one reset POST + one follow-up refetch (so the row's cooling-down
    // state clears); the in-flight tracker is cleared and no error surfaced.
    expect(resetCount).toBe(1)
    expect(getCount).toBe(2)
    expect(resetting.value).toBeNull()
    expect(resetErr.value).toBeNull()
  })

  it('surfaces a reset failure in resetError (verbatim server message)', async () => {
    resetError = true
    const { pending, resetBreaker, resetError: resetErr } = useSourceMetrics()

    await vi.waitFor(() => expect(pending.value).toBe(false))
    await resetBreaker('src-2')

    expect(resetErr.value).toBe('reset failed')
  })

  it('warmNow() POSTs /api/sources/warmup and sets the background-started message', async () => {
    const { pending, warmNow, warming, warmMessage } = useSourceMetrics()

    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(getCount).toBe(1)

    await warmNow()

    // Exactly one warm-up POST; the async pass runs in the background (202) so
    // warmNow surfaces a "started" note and clears the busy flag. The delayed
    // refetch is timer-driven and deliberately not asserted here.
    expect(postCount).toBe(1)
    expect(warming.value).toBe(false)
    expect(warmMessage.value).toBe('Warm-up started — sources warm in the background (this can take a few minutes)')
  })

  it('handles an empty metrics list gracefully', async () => {
    emptyMetrics = true
    const { metrics, pending, error } = useSourceMetrics()

    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(metrics.value).toEqual([])
    expect(error.value).toBeNull()
  })

  it('surfaces a load failure in error and a warm-up failure in warmError', async () => {
    getError = true
    const { pending, error, warmNow, warmError } = useSourceMetrics()

    await vi.waitFor(() => expect(pending.value).toBe(false))
    // A GET failure surfaces the generic load message (server message is not
    // exposed on load — matches useExtensions/useSettings).
    expect(error.value).toBe('Failed to load source metrics')

    // A warm-up POST failure surfaces the server's own message verbatim.
    postError = true
    await warmNow()
    expect(warmError.value).toBe('warm failed')
  })
})
