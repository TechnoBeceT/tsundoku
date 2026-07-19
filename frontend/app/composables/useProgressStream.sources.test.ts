/**
 * useProgressStream – sources.summary event mapping.
 *
 * Pins that a sources.summary SSE payload updates the `erroringSources` and
 * `coolingDownSources` reactive counts and reaches an `on('sources.summary', cb)`
 * subscriber. Relies on NAMED_EVENTS including 'sources.summary' — if the entry
 * were missing, addEventListener would never register for that name and neither
 * the refs nor the callback would ever fire.
 *
 * Uses a minimal EventSource stub (replacing the global) so no real SSE
 * connection is needed; `connect()` creates the stub instance, and `fire()`
 * simulates the browser dispatching a named MessageEvent.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { useProgressStream } from './useProgressStream'

// ── EventSource stub ──────────────────────────────────────────────────────────

interface StubSource {
  fire: (name: string, data: unknown) => void
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
      fire(name: string, data: unknown) {
        const ev = { data: JSON.stringify(data) } as MessageEvent
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

describe('useProgressStream – sources.summary mapping', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  it('maps sources.summary → erroringSources / coolingDownSources and calls on()', async () => {
    const { connect, on, erroringSources, coolingDownSources } = useProgressStream()
    connect()

    const received: unknown[] = []
    on('sources.summary', data => received.push(data))

    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    stubSource!.fire('sources.summary', { erroring: 2, coolingDown: 1 })

    expect(erroringSources.value).toBe(2)
    expect(coolingDownSources.value).toBe(1)
    expect(received).toHaveLength(1)
    expect(received[0]).toEqual({ erroring: 2, coolingDown: 1 })

    // A later push updates the counts (e.g. one source recovered).
    stubSource!.fire('sources.summary', { erroring: 0, coolingDown: 0 })
    expect(erroringSources.value).toBe(0)
    expect(coolingDownSources.value).toBe(0)
  })
})
