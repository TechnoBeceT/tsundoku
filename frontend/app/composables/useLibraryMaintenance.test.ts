import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useLibraryMaintenance } from './useLibraryMaintenance'

let nextOk = true
const calls: string[] = []

// useProgressStream is mocked so the tests can capture the registered
// `library.dedup.done` handler and fire it manually — that terminal SSE event is
// the ONLY report the detached sweep ever sends, so it has to be drivable.
const progressHandlers = new Map<string, Set<(d: unknown) => void>>()
function fireProgress(event: string, data: unknown): void {
  progressHandlers.get(event)?.forEach(cb => cb(data))
}

vi.mock('~/composables/useProgressStream', () => ({
  useProgressStream: () => ({
    on: (event: string, cb: (d: unknown) => void) => {
      if (!progressHandlers.has(event)) progressHandlers.set(event, new Set())
      progressHandlers.get(event)!.add(cb)
      return () => progressHandlers.get(event)?.delete(cb)
    },
    connect: vi.fn(),
    disconnect: vi.fn(),
  }),
}))

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn(),
    POST: vi.fn().mockImplementation((path: string) => {
      calls.push(path)
      if (!nextOk) {
        return Promise.resolve({ error: { message: 'sweep failed' }, response: new Response(null, { status: 500 }) })
      }
      return Promise.resolve({ data: { started: true }, error: null, response: new Response(null, { status: 202 }) })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useLibraryMaintenance', () => {
  beforeEach(() => { nextOk = true; calls.length = 0; progressHandlers.clear() })

  it('POSTs the dedup endpoint and sets the started message', async () => {
    const { dedupAllProviders, dedupAllMessage, dedupAllBusy } = useLibraryMaintenance()
    await dedupAllProviders()
    expect(calls).toContain('/api/library/dedup-providers')
    expect(dedupAllMessage.value).toBeTruthy()
    expect(dedupAllBusy.value).toBe(false)
  })

  it('surfaces a failure in dedupAllError', async () => {
    nextOk = false
    const { dedupAllProviders, dedupAllError } = useLibraryMaintenance()
    await dedupAllProviders()
    expect(dedupAllError.value).toBeTruthy()
  })
})

/**
 * The sweep is DETACHED: its HTTP response is a bare 202, so without the
 * terminal `library.dedup.done` event the dialog would stop at "started" and the
 * owner would never learn what happened (§16 — a silent operation).
 */
describe('useLibraryMaintenance — the detached sweep\'s terminal report', () => {
  beforeEach(() => { nextOk = true; calls.length = 0; progressHandlers.clear() })

  it('replaces the started line with the real outcome when the sweep lands', async () => {
    const { dedupAllProviders, dedupAllMessage, dedupAllSkippedBusy } = useLibraryMaintenance()
    await dedupAllProviders()
    expect(dedupAllMessage.value).toContain('Dedup started')

    fireProgress('library.dedup.done', { seriesProcessed: 12, merged: 2, skipped: 0, busy: 0 })

    expect(dedupAllMessage.value).toContain('merged 2 duplicate sources')
    expect(dedupAllSkippedBusy.value).toBe(0)
  })

  it('exposes the busy-skip count separately so the dialog can act on it', async () => {
    const { dedupAllProviders, dedupAllSkippedBusy, dedupAllMessage } = useLibraryMaintenance()
    await dedupAllProviders()

    fireProgress('library.dedup.done', { seriesProcessed: 9, merged: 1, skipped: 0, busy: 3 })

    expect(dedupAllSkippedBusy.value).toBe(3)
    // Never folded into the summary sentence — the dialog renders its own line.
    expect(dedupAllMessage.value).not.toContain('3')
  })

  it('surfaces a sweep failure and never swallows it', async () => {
    const { dedupAllProviders, dedupAllError, dedupAllMessage } = useLibraryMaintenance()
    await dedupAllProviders()

    fireProgress('library.dedup.done', { seriesProcessed: 4, merged: 0, skipped: 0, busy: 1, error: 'the clean-up failed — see the server log for details' })

    expect(dedupAllError.value).toContain('the clean-up failed')
    expect(dedupAllMessage.value).toBeNull()
  })

  it('clears the previous run\'s outcome when a new sweep starts', async () => {
    const { dedupAllProviders, dedupAllSkippedBusy } = useLibraryMaintenance()
    await dedupAllProviders()
    fireProgress('library.dedup.done', { seriesProcessed: 9, merged: 1, skipped: 0, busy: 3 })
    expect(dedupAllSkippedBusy.value).toBe(3)

    await dedupAllProviders()
    expect(dedupAllSkippedBusy.value).toBe(0)
  })
})
