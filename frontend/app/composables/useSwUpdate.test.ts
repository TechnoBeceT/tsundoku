/**
 * useSwUpdate — waiting-worker detection + SKIP_WAITING apply.
 *
 * Pins:
 *   1. An update (updatefound → installing 'installed' WHILE a controller
 *      exists) flips `updateAvailable` and applyUpdate posts SKIP_WAITING.
 *   2. A FIRST install (no existing controller) does NOT prompt.
 *
 * Non-vacuous: if the statechange listener were not wired, assertion 1's
 * updateAvailable stays false; if applyUpdate skipped the postMessage, the spy
 * stays uncalled. The module is a singleton, so each test re-imports it fresh.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'

interface FakeWorker extends EventTarget { state: string, postMessage: ReturnType<typeof vi.fn> }

function makeWorker(state: string): FakeWorker {
  const w = new EventTarget() as FakeWorker
  w.state = state
  w.postMessage = vi.fn()
  return w
}

function installServiceWorker(controller: object | null): void {
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: Object.assign(new EventTarget(), { controller }),
  })
}

beforeEach(() => {
  vi.resetModules()
})

describe('useSwUpdate', () => {
  it('detects a waiting update and applyUpdate posts SKIP_WAITING', async () => {
    installServiceWorker({})
    const { useSwUpdate } = await import('./useSwUpdate')
    const { updateAvailable, watch, applyUpdate } = useSwUpdate()

    const installing = makeWorker('installing')
    const reg = Object.assign(new EventTarget(), { installing, waiting: null }) as unknown as ServiceWorkerRegistration
    watch(reg)

    reg.dispatchEvent(new Event('updatefound'))
    installing.state = 'installed'
    installing.dispatchEvent(new Event('statechange'))

    expect(updateAvailable.value).toBe(true)

    applyUpdate()
    expect(installing.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' })
    expect(updateAvailable.value).toBe(false)
  })

  it('does not prompt on a first install (no existing controller)', async () => {
    installServiceWorker(null)
    const { useSwUpdate } = await import('./useSwUpdate')
    const { updateAvailable, watch } = useSwUpdate()

    const installing = makeWorker('installing')
    const reg = Object.assign(new EventTarget(), { installing, waiting: null }) as unknown as ServiceWorkerRegistration
    watch(reg)

    reg.dispatchEvent(new Event('updatefound'))
    installing.state = 'installed'
    installing.dispatchEvent(new Event('statechange'))

    expect(updateAvailable.value).toBe(false)
  })
})
