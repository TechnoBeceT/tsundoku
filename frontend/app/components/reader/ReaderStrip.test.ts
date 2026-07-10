/**
 * ReaderStrip — DOM-layer (happy-dom) mount tests for the orchestration the pure
 * logic can't cover: the IntersectionObserver append wiring, the pageCount
 * tail-404 trim + its scroll-anchor compensation (Fix A), the mid-vs-tail failure
 * distinction, once-only chapter-finished, and the 0-visible-page finished skip
 * (Fix D).
 *
 * getBoundingClientRect / IntersectionObserver / scrollTop are stubbed — happy-dom
 * has no layout — so the tests drive the real component wiring against controlled
 * geometry, not real pixels.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import ReaderStrip from './ReaderStrip.vue'
import type { ReaderChapter } from '~/composables/useReader'

const chA: ReaderChapter = { id: 'ch-A', number: 1, name: 'A', pageCount: 3, read: false, lastReadPage: 0 }
const chB: ReaderChapter = { id: 'ch-B', number: 2, name: 'B', pageCount: 2, read: false, lastReadPage: 0 }
const chC: ReaderChapter = { id: 'ch-C', number: 3, name: 'C', pageCount: 2, read: false, lastReadPage: 0 }
const chZ: ReaderChapter = { id: 'ch-Z', number: 9, name: 'Z', pageCount: 0, read: false, lastReadPage: 0 }

const pageUrl = (id: string, n: number): string => `x/${id}/${n}`

// Captured IntersectionObserver callback (fire "sentinel visible") + observed target.
let ioCallback: IntersectionObserverCallback | null = null
let observedEl: Element | null = null

class IOStub {
  constructor(cb: IntersectionObserverCallback) { ioCallback = cb }
  observe(el: Element): void { observedEl = el }
  disconnect(): void { observedEl = null }
}

/** A DOMRect-like at a given top/height (only top/height matter for the math). */
function rect(top: number, height = 0): DOMRect {
  return { top, bottom: top + height, height, left: 0, right: 0, width: 0, x: 0, y: top, toJSON: () => ({}) }
}

/** Overrides an element's getBoundingClientRect with a fixed top/height. */
function stubRect(el: Element, top: number, height = 0): void {
  el.getBoundingClientRect = () => rect(top, height)
}

/** Makes scrollTop a writable/readable property backed by a local var. */
function makeScrollable(el: HTMLElement, initial: number): void {
  let v = initial
  Object.defineProperty(el, 'scrollTop', { configurable: true, get: () => v, set: (nv: number) => { v = nv } })
}

function pageDivs(wrapper: ReturnType<typeof mount>, chapterId: string): number {
  return wrapper.findAll(`[data-chapter-id="${chapterId}"][data-page]`).length
}

beforeEach(() => {
  ioCallback = null
  observedEl = null
  vi.stubGlobal('IntersectionObserver', IOStub)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ReaderStrip — rendering', () => {
  it('renders visiblePages pages per chapter, a divider after each, and observes the sentinel', () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chA, chB, chC], mountedChapters: [chA, chB], pageUrl },
    })
    expect(wrapper.findAll('[data-page]').length).toBe(chA.pageCount + chB.pageCount)
    expect(wrapper.findAll('[data-divider-id]').length).toBe(2)
    expect(observedEl).not.toBeNull()
  })
})

describe('ReaderStrip — pageCount tail-404 tolerance', () => {
  it('trims the trailing failed page AND preserves the read position (Fix A)', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chA, chB, chC], mountedChapters: [chA, chB], pageUrl },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 1000)
    stubRect(container, 0)
    // Anchor = last mounted chapter (ch-B). Its content-top tracks how many ch-A
    // pages are currently rendered (each 300px tall), so trimming a ch-A page
    // moves the anchor UP by 300 and the anchor math must drop scrollTop by 300.
    const bEl = wrapper.find('[data-chapter-id="ch-B"]').element
    bEl.getBoundingClientRect = () => rect(pageDivs(wrapper, 'ch-A') * 300)

    // Fail ch-A's LAST page (index 2 of 3) — the contiguous trailing tail.
    await wrapper.find('[data-chapter-id="ch-A"][data-page="2"] img').trigger('error')
    await nextTick()
    await flushPromises()

    expect(pageDivs(wrapper, 'ch-A')).toBe(2) // trimmed
    expect(container.scrollTop).toBe(700) // 1000 + (1600 - 1900), no jump
  })

  it('keeps a mid-chapter failure as a placeholder and does NOT trim or move', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chA, chB, chC], mountedChapters: [chA, chB], pageUrl },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 500)
    stubRect(container, 0)
    const bEl = wrapper.find('[data-chapter-id="ch-B"]').element
    bEl.getBoundingClientRect = () => rect(pageDivs(wrapper, 'ch-A') * 300)

    // Fail ch-A's MIDDLE page (index 1) — a real error, not the tail.
    await wrapper.find('[data-chapter-id="ch-A"][data-page="1"] img').trigger('error')
    await nextTick()
    await flushPromises()

    expect(pageDivs(wrapper, 'ch-A')).toBe(3) // NOT trimmed
    expect(wrapper.text()).toContain('Page unavailable') // placeholder shown
    expect(container.scrollTop).toBe(500) // no reflow, no jump
  })
})

describe('ReaderStrip — append', () => {
  it('emits near-tail when the sentinel intersects and a next chapter exists', () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chA, chB, chC], mountedChapters: [chA, chB], pageUrl },
    })
    ioCallback?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-tail')).toHaveLength(1)
  })

  it('does NOT emit near-tail at the last chapter (no next)', () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chA, chB], mountedChapters: [chB], pageUrl },
    })
    ioCallback?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-tail')).toBeUndefined()
  })
})

describe('ReaderStrip — chapter-finished', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('emits chapter-finished once per chapter even across repeated scrolls', () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chA, chB], mountedChapters: [chA], pageUrl },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 5000)
    stubRect(container, 0)
    // ch-A's divider is scrolled above the viewport top (rect.top negative).
    stubRect(wrapper.find('[data-divider-id="ch-A"]').element, -100)

    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200) // trailing throttle fires runScroll #1
    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200) // runScroll #2 — must NOT re-emit

    expect(wrapper.emitted('chapter-finished')).toHaveLength(1)
    expect(wrapper.emitted('chapter-finished')![0]).toEqual(['ch-A'])
  })

  it('never finishes a chapter with zero visible pages (Fix D)', () => {
    const wrapper = mount(ReaderStrip, {
      props: { chapters: [chZ, chA], mountedChapters: [chZ], pageUrl },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 5000)
    stubRect(container, 0)
    stubRect(wrapper.find('[data-divider-id="ch-Z"]').element, -100) // "above", but 0 pages

    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200)

    expect(wrapper.emitted('chapter-finished')).toBeUndefined()
  })
})
