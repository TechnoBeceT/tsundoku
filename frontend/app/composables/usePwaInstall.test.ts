/**
 * usePwaInstall — beforeinstallprompt capture, prompt replay, install hide.
 *
 * Pins:
 *   1. A dispatched `beforeinstallprompt` flips `installable` true.
 *   2. `promptInstall()` calls the stashed event's `prompt()` and hides.
 *   3. An `appinstalled` event hides the button.
 *
 * Non-vacuous: if the listener were not registered, assertion 1's installable
 * stays false; if promptInstall didn't replay, the prompt spy stays uncalled.
 *
 * The composable registers mount/unmount listeners, so it runs inside a mounted
 * harness. matchMedia is stubbed to report "not standalone".
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { usePwaInstall } from './usePwaInstall'

function harness() {
  let api!: ReturnType<typeof usePwaInstall>
  const Comp = defineComponent({
    setup() {
      api = usePwaInstall()
      return () => null
    },
  })
  mount(Comp)
  return () => api
}

/** Build a synthetic beforeinstallprompt with a spyable prompt(). */
function makeInstallEvent() {
  const prompt = vi.fn(() => Promise.resolve())
  const ev = Object.assign(new Event('beforeinstallprompt'), { prompt, userChoice: Promise.resolve({ outcome: 'accepted' as const }) })
  return { ev, prompt }
}

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: (query: string) => ({ matches: false, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
  })
})

describe('usePwaInstall', () => {
  it('flips installable true on beforeinstallprompt', async () => {
    const api = harness()
    const { ev } = makeInstallEvent()
    window.dispatchEvent(ev)
    await flushPromises()
    expect(api().installable.value).toBe(true)
  })

  it('replays the stashed prompt on promptInstall and hides', async () => {
    const api = harness()
    const { ev, prompt } = makeInstallEvent()
    window.dispatchEvent(ev)
    await flushPromises()

    await api().promptInstall()
    expect(prompt).toHaveBeenCalledTimes(1)
    expect(api().installable.value).toBe(false)
  })

  it('hides on appinstalled', async () => {
    const api = harness()
    const { ev } = makeInstallEvent()
    window.dispatchEvent(ev)
    await flushPromises()
    expect(api().installable.value).toBe(true)

    window.dispatchEvent(new Event('appinstalled'))
    await flushPromises()
    expect(api().installable.value).toBe(false)
  })
})
