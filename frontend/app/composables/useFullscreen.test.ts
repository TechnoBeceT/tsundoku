/**
 * useFullscreen — mount-level tests for the feature-detected Fullscreen wrapper.
 *
 * Pins:
 *   1. `supported` reflects `document.fullscreenEnabled` (both ways).
 *   2. `toggle` requests fullscreen on the element when none is active.
 *   3. `toggle` exits when an element is already fullscreen.
 *   4. `isFullscreen` tracks the `fullscreenchange` event (incl. Esc/OS exit).
 *   5. A rejected request is swallowed (best-effort, never throws).
 *
 * The composable registers a mount/unmount listener, so it is exercised inside a
 * mounted harness component. `document.fullscreenElement` / `fullscreenEnabled`
 * and the request/exit calls are stubbed — happy-dom has no real fullscreen.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { useFullscreen } from './useFullscreen'

let fsElement: Element | null = null
let enabled = true
const requestFullscreen = vi.fn<() => Promise<void>>(() => Promise.resolve())
const exitFullscreen = vi.fn<() => Promise<void>>(() => Promise.resolve())

beforeEach(() => {
  fsElement = null
  enabled = true
  requestFullscreen.mockClear().mockResolvedValue(undefined)
  exitFullscreen.mockClear().mockResolvedValue(undefined)
  Object.defineProperty(document, 'fullscreenElement', { configurable: true, get: () => fsElement })
  Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, get: () => enabled })
  Object.defineProperty(document, 'exitFullscreen', { configurable: true, value: exitFullscreen })
})

afterEach(() => {
  // @ts-expect-error — remove the stubbed fullscreenElement accessor between tests.
  delete document.fullscreenElement
  // @ts-expect-error — remove the stubbed fullscreenEnabled accessor between tests.
  delete document.fullscreenEnabled
})

/** Mount a harness whose setup runs useFullscreen, and hand the API back. */
function setup(): ReturnType<typeof useFullscreen> {
  let api!: ReturnType<typeof useFullscreen>
  mount(defineComponent({ setup() { api = useFullscreen(); return () => h('div') } }))
  return api
}

/** A detached element whose requestFullscreen is the shared stub. */
function makeEl(): HTMLElement {
  const el = document.createElement('div')
  el.requestFullscreen = requestFullscreen
  return el
}

describe('useFullscreen — support detection', () => {
  it('reports supported when the API is enabled', () => {
    enabled = true
    expect(setup().supported.value).toBe(true)
  })

  it('reports unsupported when the API is disabled', () => {
    enabled = false
    expect(setup().supported.value).toBe(false)
  })
})

describe('useFullscreen — toggle', () => {
  it('requests fullscreen on the element when none is active', async () => {
    const { toggle } = setup()
    const el = makeEl()
    await toggle(el)
    expect(requestFullscreen).toHaveBeenCalledOnce()
    expect(exitFullscreen).not.toHaveBeenCalled()
  })

  it('exits fullscreen when an element is already fullscreen', async () => {
    const { toggle } = setup()
    const el = makeEl()
    fsElement = el
    await toggle(el)
    expect(exitFullscreen).toHaveBeenCalledOnce()
    expect(requestFullscreen).not.toHaveBeenCalled()
  })

  it('swallows a rejected request without throwing', async () => {
    requestFullscreen.mockRejectedValueOnce(new Error('denied'))
    const { toggle } = setup()
    await expect(toggle(makeEl())).resolves.toBeUndefined()
  })
})

describe('useFullscreen — state sync', () => {
  it('tracks the fullscreenchange event', async () => {
    const { isFullscreen } = setup()
    expect(isFullscreen.value).toBe(false)

    fsElement = document.createElement('div')
    document.dispatchEvent(new Event('fullscreenchange'))
    await flushPromises()
    expect(isFullscreen.value).toBe(true)

    fsElement = null
    document.dispatchEvent(new Event('fullscreenchange'))
    await flushPromises()
    expect(isFullscreen.value).toBe(false)
  })
})
