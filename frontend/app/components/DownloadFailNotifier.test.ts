/**
 * DownloadFailNotifier — the app-global download-failure toast.
 *
 * Pins:
 *   1. A download.fail SSE event surfaces the danger toast with the error body.
 *   2. Failures arriving inside the throttle window AGGREGATE into one
 *      "N downloads failed" toast (leading + trailing edge), not a storm.
 *
 * Non-vacuous: drop the on('download.fail') subscription and no toast appears;
 * drop the aggregation bucket and the second toast reads "Download failed" (1),
 * never "2 downloads failed".
 */
import { describe, it, expect, vi, beforeAll, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import DownloadFailNotifier from './DownloadFailNotifier.vue'
import { useProgressStream } from '~/composables/useProgressStream'

interface StubSource { fire: (name: string, payload: unknown) => void }
let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  private _handlers = new Map<string, ((ev: Event) => void)[]>()
  constructor(_url: string) {
    const handlers = this._handlers
    const onOpenRef = (): void => { this.onopen?.(new Event('open')) }
    stubSource = {
      fire(name: string, payload: unknown) {
        const ev = { data: JSON.stringify(payload) } as MessageEvent
        ;(handlers.get(name) ?? []).forEach((h) => h(ev))
      },
    }
    queueMicrotask(onOpenRef)
  }

  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }

  removeEventListener(): void { void 0 }
  close(): void { stubSource = null }
}

describe('DownloadFailNotifier', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    useProgressStream().connect()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows a danger toast with the error body on download.fail', async () => {
    const wrapper = mount(DownloadFailNotifier)
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    stubSource!.fire('download.fail', { chapter_id: 'c1', state: 'failed', error: 'Cloudflare block' })
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Download failed')
    expect(wrapper.text()).toContain('Cloudflare block')
    wrapper.unmount()
  })

  it('aggregates failures within the throttle window into one "N downloads failed"', async () => {
    vi.useFakeTimers()
    const wrapper = mount(DownloadFailNotifier)
    // The stub already exists from the previous mount's connect().
    expect(stubSource).not.toBeNull()

    // Leading edge: first failure shows immediately (count 1).
    stubSource!.fire('download.fail', { chapter_id: 'a', state: 'failed', error: 'first' })
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Download failed')

    // Two more during the cooldown window → counted, not shown yet.
    stubSource!.fire('download.fail', { chapter_id: 'b', state: 'failed', error: 'second' })
    stubSource!.fire('download.fail', { chapter_id: 'c', state: 'permanently_failed', error: 'third' })

    // Advancing past the throttle fires the trailing aggregated toast.
    await vi.advanceTimersByTimeAsync(8000)
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('2 downloads failed')
    wrapper.unmount()
  })
})
