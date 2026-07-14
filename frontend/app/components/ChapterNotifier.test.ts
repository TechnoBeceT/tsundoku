/**
 * ChapterNotifier — the app-global in-app chapter.new toast.
 *
 * Pins the focus-gating fix (strictly complementary to the service worker's
 * `focused` push-suppression): the in-app toast shows ONLY when the window is
 * FOCUSED (document.hasFocus()), so a visible-but-unfocused desktop window never
 * gets BOTH the OS push and the toast.
 *
 * Non-vacuous: if the guard used visibilityState (or no guard), the unfocused
 * case would still render the toast and the second assertion would fail.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import { nextTick } from 'vue'
import ChapterNotifier from './ChapterNotifier.vue'

const h = vi.hoisted(() => ({ cb: null as null | ((d: unknown) => void) }))

mockNuxtImport('useProgressStream', () => () => ({
  connect: vi.fn(),
  on: (_event: string, fn: (d: unknown) => void) => {
    h.cb = fn
    return vi.fn()
  },
}))
mockNuxtImport('navigateTo', () => () => Promise.resolve())

const payload = {
  groups: [{ seriesId: 's1', title: 'Solo Leveling', count: 1, url: '/series/s1' }],
  total: 1,
  digest: false,
  title: 'Solo Leveling',
  body: '1 new chapter',
}

function setFocus(focused: boolean): void {
  Object.defineProperty(document, 'hasFocus', { configurable: true, value: () => focused })
}

beforeEach(() => {
  h.cb = null
})

describe('ChapterNotifier', () => {
  it('shows the toast when the window is focused', async () => {
    setFocus(true)
    const wrapper = await mountSuspended(ChapterNotifier)
    h.cb?.(payload)
    await nextTick()
    expect(wrapper.html()).toContain('1 new chapter')
  })

  it('does NOT show the toast when the window is unfocused', async () => {
    setFocus(false)
    const wrapper = await mountSuspended(ChapterNotifier)
    h.cb?.(payload)
    await nextTick()
    expect(wrapper.html()).not.toContain('1 new chapter')
  })
})
