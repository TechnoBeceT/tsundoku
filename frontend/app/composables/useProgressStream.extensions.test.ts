/**
 * useProgressStream – extensions.checked event registration.
 *
 * Pins the behaviour that `on('extensions.checked', cb)` receives a payload
 * when the server fires the event. Relies on NAMED_EVENTS including
 * 'extensions.checked' — if the entry were missing, `addEventListener` would
 * never be called for that name and the payload would never reach `cb`.
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
    // Capture the handlers map by reference — addEventListener populates it
    // and fire() reads it through the same reference (Maps are objects).
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

describe('useProgressStream – extensions.checked registration', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  it('on("extensions.checked", cb) is invoked when the event fires', async () => {
    const { connect, on } = useProgressStream()
    connect()

    const received: unknown[] = []
    on('extensions.checked', data => received.push(data))

    // Wait for the stub to be created (queueMicrotask in constructor).
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    // Fire the event through the stub.
    stubSource!.fire('extensions.checked', { checkedAt: '2026-06-29T00:00:00Z' })

    expect(received).toHaveLength(1)
    expect(received[0]).toEqual({ checkedAt: '2026-06-29T00:00:00Z' })
  })
})
