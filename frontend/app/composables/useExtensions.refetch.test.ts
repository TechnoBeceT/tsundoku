/**
 * useExtensions – refetch on extensions.checked SSE.
 *
 * Pins the behaviour that when `extensions.checked` fires on the SSE stream,
 * useExtensions calls GET /api/suwayomi/extensions again (the list refreshes).
 *
 * Non-vacuous: if the `on('extensions.checked', ...)` subscription were removed,
 * the extensions GET would only be called once (initial load) and the assertion
 * `extGetCount > countAfterInit` would fail.
 *
 * Uses the same FakeEventSource pattern as useProgressStream.extensions.test.ts
 * so the NAMED_EVENTS loop registers an addEventListener for 'extensions.checked',
 * which our stub can then fire. useProgressStream is a module singleton in this
 * test file so useExtensions (auto-imported via Nuxt) shares the same listeners
 * map — firing through the stub calls the subscription registered by useExtensions.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { useExtensions } from './useExtensions'
import { useProgressStream } from './useProgressStream'

// ── Call tracking ─────────────────────────────────────────────────────────────

let extGetCount = 0

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/suwayomi/extensions') {
        extGetCount++
        return Promise.resolve({ data: [], error: null })
      }
      if (path === '/api/suwayomi/extensions/repos') {
        return Promise.resolve({ data: { repos: [] }, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── EventSource stub ──────────────────────────────────────────────────────────

interface StubSource {
  fire: (name: string) => void
}

let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  private _handlers = new Map<string, ((ev: Event) => void)[]>()

  constructor(_url: string) {
    const handlers = this._handlers
    const onOpenRef = () => this.onopen?.(new Event('open'))

    stubSource = {
      fire(name: string) {
        const ev = { data: '{}' } as MessageEvent
        ;(handlers.get(name) ?? []).forEach(h => h(ev))
      },
    }
    queueMicrotask(onOpenRef)
  }

  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }

  removeEventListener(_name?: string, _handler?: (ev: Event) => void): void { void 0 }
  close(): void { stubSource = null }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useExtensions – refetch on extensions.checked', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    // Connect the singleton stream so the stub is created before useExtensions
    // registers its listener. This is idempotent — subsequent connect() calls
    // from useProgressStream are no-ops.
    useProgressStream().connect()
  })

  it('re-fetches the extension list when extensions.checked fires', async () => {
    extGetCount = 0

    const { pending } = useExtensions()

    // Wait for the initial load (fires GET /api/suwayomi/extensions once).
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(extGetCount).toBeGreaterThanOrEqual(1)

    const countAfterInit = extGetCount

    // Wait for the stub to be ready (created in FakeEventSource constructor).
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    // Fire the SSE event — the subscription in useExtensions calls refresh().
    stubSource!.fire('extensions.checked')

    // Assert the list was re-fetched.
    await vi.waitFor(() => {
      expect(extGetCount).toBeGreaterThan(countAfterInit)
    })
  })
})
