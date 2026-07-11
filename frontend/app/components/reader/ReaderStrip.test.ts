/**
 * ReaderStrip — DOM-layer (happy-dom) mount tests for the orchestration the pure
 * logic can't cover: the IntersectionObserver append/prepend wiring, the
 * reflow-anchor bracket (now anchored on the CENTRED chapter, not the tail — see
 * the dedicated describe block below, the #1 regression risk of this slice), the
 * pageCount tail-404 trim + its scroll-anchor compensation (Fix A), the mid-vs-
 * tail failure distinction, `scrollRequest` token handling (replacing the old
 * one-shot `didInitialScroll` fuse), `seekToPage`, the `visible-pages` emit, and
 * once-only `chapter-finished` (incl. its reset on a fresh `scrollRequest`).
 *
 * getBoundingClientRect / IntersectionObserver / scrollTop / clientHeight are
 * stubbed — happy-dom has no layout — so the tests drive the real component
 * wiring against controlled geometry, not real pixels.
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

/** The props every test needs regardless of scenario — `hasPrev`/`scrollRequest`
 *  are required props (Task 4), so every `mount` spreads this in and overrides
 *  what the scenario needs. */
const base = { pageUrl, hasPrev: false, scrollRequest: null } as const

// Captured IntersectionObserver callback (fire "sentinel visible") + observed targets.
let ioCallback: IntersectionObserverCallback | null = null
let observedEls: Element[] = []

class IOStub {
  constructor(cb: IntersectionObserverCallback) { ioCallback = cb }
  observe(el: Element): void { observedEls.push(el) }
  disconnect(): void { observedEls = [] }
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

/** Stubs a fixed clientHeight (happy-dom has no real layout). */
function stubClientHeight(el: HTMLElement, height: number): void {
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: height })
}

/** Stubs every `[data-page]` element as stacked `pageHeight`-tall blocks, in
 *  document order — gives `centeredPage()` real geometry to reason over. The
 *  stub is SCROLL-AWARE: `runScroll`/`contentTop` both read `getBoundingClientRect
 *  ().top` (viewport-relative) and separately ADD the live `container.scrollTop`
 *  to recover an absolute content position, exactly like a real scrolled layout
 *  (where `rect.top` shrinks as `scrollTop` grows). A STATIC stub would only be
 *  correct at `scrollTop === 0` — this ties each page's absolute top
 *  (`i * pageHeight`) to the container's CURRENT scrollTop, so the geometry stays
 *  consistent across every scroll position a test sets. */
function stubPagesSequentially(wrapper: ReturnType<typeof mount>, container: HTMLElement, pageHeight = 300): void {
  wrapper.findAll('[data-page]').forEach((p, i) => {
    const absoluteTop = i * pageHeight
    p.element.getBoundingClientRect = () => rect(absoluteTop - container.scrollTop, pageHeight)
  })
}

function pageDivs(wrapper: ReturnType<typeof mount>, chapterId: string): number {
  return wrapper.findAll(`[data-chapter-id="${chapterId}"][data-page]`).length
}

/** Exposes `seekToPage` off the mounted instance (defineExpose isn't typed here). */
function seekToPageOf(wrapper: ReturnType<typeof mount>): (page: number) => void {
  return (wrapper.vm as unknown as { seekToPage: (page: number) => void }).seekToPage
}

beforeEach(() => {
  ioCallback = null
  observedEls = []
  vi.stubGlobal('IntersectionObserver', IOStub)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ReaderStrip — rendering', () => {
  it('renders visiblePages pages per chapter, a divider after each, and observes both sentinels', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB] },
    })
    expect(wrapper.findAll('[data-page]').length).toBe(chA.pageCount + chB.pageCount)
    expect(wrapper.findAll('[data-divider-id]').length).toBe(2)
    expect(observedEls).toHaveLength(2)
    expect(wrapper.find('[data-sentinel="head"]').exists()).toBe(true)
    expect(wrapper.find('[data-sentinel="tail"]').exists()).toBe(true)
  })
})

describe('ReaderStrip — pageCount tail-404 tolerance', () => {
  it('trims the trailing failed page AND preserves the read position (Fix A)', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 1000)
    stubRect(container, 0)
    // Nothing has been centred yet, so the anchor falls back to the tail (ch-B).
    // Its content-top tracks how many ch-A pages are currently rendered (each
    // 300px tall), so trimming a ch-A page moves the anchor UP by 300 and the
    // anchor math must drop scrollTop by 300.
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
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB] },
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

describe('ReaderStrip — scrollRequest (replaces the initialScrollTo one-shot fuse)', () => {
  it('scrolls to the requested page offset once on initial mount', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA], scrollRequest: { chapterId: 'ch-A', page: 2, token: 1 } },
    })
    // Stub geometry AFTER mount: applyScrollRequest awaits nextTick before
    // reading rects, so the stubs are in place by the time it resolves.
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    // ch-A page 2 sits 600px down (each rendered page 300px tall).
    stubRect(wrapper.find('[data-chapter-id="ch-A"][data-page="2"]').element, 600)

    await nextTick()
    await flushPromises()

    expect(container.scrollTop).toBe(600)
  })

  it('falls back to the chapter top when the requested page is not mounted', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA], scrollRequest: { chapterId: 'ch-A', page: 99, token: 1 } },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    // Page 99 doesn't exist → the chapter wrapper (content-top 120) is the
    // fallback anchor, so scrollTop lands on the chapter top.
    stubRect(wrapper.find('.strip__chapter[data-chapter-id="ch-A"]').element, 120)

    await nextTick()
    await flushPromises()

    expect(container.scrollTop).toBe(120)
  })

  it('does not scroll when scrollRequest is null', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 250)
    stubRect(container, 0)

    await nextTick()
    await flushPromises()

    expect(container.scrollTop).toBe(250) // untouched
  })

  it('ignores a re-render carrying the SAME token (a manual scroll is not overridden)', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA], scrollRequest: { chapterId: 'ch-A', page: 0, token: 1 } },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    stubRect(wrapper.find('[data-chapter-id="ch-A"][data-page="0"]').element, 50)
    await nextTick()
    await flushPromises()
    expect(container.scrollTop).toBe(50)

    container.scrollTop = 999 // the reader scrolled away manually
    await wrapper.setProps({ scrollRequest: { chapterId: 'ch-A', page: 0, token: 1 } }) // new object, SAME token
    await flushPromises()

    expect(container.scrollTop).toBe(999) // untouched — the token didn't change
  })

  it('scrolls again when a NEW token arrives (e.g. a later chapter jump)', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA], scrollRequest: { chapterId: 'ch-A', page: 0, token: 1 } },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    stubRect(wrapper.find('[data-chapter-id="ch-A"][data-page="0"]').element, 50)
    await nextTick()
    await flushPromises()
    expect(container.scrollTop).toBe(50)

    // contentTop adds the CURRENT scrollTop (50 at this point) to the stubbed
    // rect.top, so target a final scrollTop of 350 by stubbing 300 (350 - 50).
    stubRect(wrapper.find('[data-chapter-id="ch-A"][data-page="1"]').element, 300)
    await wrapper.setProps({ scrollRequest: { chapterId: 'ch-A', page: 1, token: 2 } })
    await flushPromises()

    expect(container.scrollTop).toBe(350)
  })
})

describe('ReaderStrip — tail sentinel (append)', () => {
  it('emits near-tail when the sentinel intersects and a next chapter exists', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB] },
    })
    const target = wrapper.find('[data-sentinel="tail"]').element
    ioCallback?.([{ isIntersecting: true, target } as unknown as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-tail')).toHaveLength(1)
  })

  it('does NOT emit near-tail at the last chapter (no next)', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB], mountedChapters: [chB] },
    })
    const target = wrapper.find('[data-sentinel="tail"]').element
    ioCallback?.([{ isIntersecting: true, target } as unknown as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-tail')).toBeUndefined()
  })
})

describe('ReaderStrip — head sentinel (prepend)', () => {
  it('emits near-head, bracketed, when the sentinel intersects and hasPrev is true', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chB, chC], hasPrev: true },
    })
    const target = wrapper.find('[data-sentinel="head"]').element
    ioCallback?.([{ isIntersecting: true, target } as unknown as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-head')).toHaveLength(1)
  })

  it('does NOT emit near-head when hasPrev is false (nothing to prepend)', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB], hasPrev: false },
    })
    const target = wrapper.find('[data-sentinel="head"]').element
    ioCallback?.([{ isIntersecting: true, target } as unknown as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-head')).toBeUndefined()
  })
})

describe('ReaderStrip — reflow anchor: the CENTRED chapter survives what the tail does not', () => {
  it('does not jump when a head-prepend reflow unmounts the OLD tail chapter', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB, chC], hasPrev: true },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    stubClientHeight(container, 100)
    stubPagesSequentially(wrapper, container, 300)
    // ch-A pages 0,1,2 → tops 0,300,600 · ch-B pages 0,1 → tops 900,1200 ·
    // ch-C pages 0,1 → tops 1500,1800 (all 300px tall, in document order).
    container.scrollTop = 1250 // mid = 1300 → lands in ch-B's second page [1200,1500)
    container.dispatchEvent(new Event('scroll')) // first scroll on a fresh instance runs immediately
    expect(wrapper.emitted('centered')?.at(-1)).toEqual([{ chapterId: 'ch-B', page: 1 }])

    // The reflow-anchor math reads the CH-B *chapter wrapper's* rect (the
    // element `[data-chapter-id="ch-B"]` resolves to, per document order) —
    // independent of the individual page rects stubbed above. Stubbed as a LIVE
    // function of how many pages currently render before it (300px each), so it
    // stays correct at WHATEVER moment afterReflow's `await nextTick()` actually
    // resolves relative to the `setProps` below — no assumption about exact
    // microtask interleaving is needed (mirrors the Fix-A test's approach).
    const bWrapper = wrapper.find('.strip__chapter[data-chapter-id="ch-B"]').element
    bWrapper.getBoundingClientRect = () => rect((pageDivs(wrapper, 'ch-0') + pageDivs(wrapper, 'ch-A')) * 300)
    // Before the prepend: only ch-A's 3 pages precede ch-B → 900.

    // Fire the head sentinel — beforeReflow() snapshots ch-B's pre-reflow
    // position (900) + the current scrollTop (1250) right now.
    const target = wrapper.find('[data-sentinel="head"]').element
    ioCallback?.([{ isIntersecting: true, target } as unknown as IntersectionObserverEntry], {} as IntersectionObserver)
    expect(wrapper.emitted('near-head')).toHaveLength(1)

    // Simulate the composable's reaction to the prepend: a new chapter is added
    // above AND — because the mounted window is already at its cap — the OLD
    // TAIL (ch-C) is unmounted. If the anchor were still the tail (the pre-slice
    // bug), it would now be GONE and afterReflow() would find no anchor at all.
    const ch0: ReaderChapter = { id: 'ch-0', number: 0, name: 'Zero', pageCount: 1, read: false, lastReadPage: 0 }
    await wrapper.setProps({ mountedChapters: [ch0, chA, chB] })
    await flushPromises() // let afterReflow's pending nextTick resolve

    expect(wrapper.find('[data-chapter-id="ch-C"]').exists()).toBe(false) // the old tail is really gone

    // After the prepend: ch-0 (1 page) + ch-A (3 pages) precede ch-B → 1200.
    // scrollTop shifts by exactly the anchor's delta (1200 - 900 = 300) — the
    // pre-reflow read position stays visually fixed even though the OLD tail
    // vanished. Anchoring on the tail instead would have found no ch-C element,
    // returned early, and left scrollTop stuck at 1250 — a visible jump.
    expect(container.scrollTop).toBe(1250 + 300)
  })
})

describe('ReaderStrip — visible-pages emit', () => {
  it('emits the centred chapter\'s TRIMMED page count on scroll, only when it changes', () => {
    vi.useFakeTimers()
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB], mountedChapters: [chA, chB] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    stubClientHeight(container, 100)
    stubPagesSequentially(wrapper, container, 300) // ch-A: 0,1,2 → 0/300/600 · ch-B: 0,1 → 900/1200

    container.scrollTop = 50 // mid = 100 → ch-A page 0
    container.dispatchEvent(new Event('scroll')) // fresh instance → runs immediately
    expect(wrapper.emitted('visible-pages')).toEqual([[{ chapterId: 'ch-A', count: 3 }]])

    container.scrollTop = 350 // mid = 400 → still ch-A (page 1), same trimmed count
    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200)
    expect(wrapper.emitted('visible-pages')).toHaveLength(1) // unchanged → no re-emit

    container.scrollTop = 950 // mid = 1000 → ch-B page 0, a DIFFERENT chapter/count
    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200)
    expect(wrapper.emitted('visible-pages')).toHaveLength(2)
    expect(wrapper.emitted('visible-pages')?.at(-1)).toEqual([{ chapterId: 'ch-B', count: 2 }])

    vi.useRealTimers()
  })
})

describe('ReaderStrip — seekToPage (exposed)', () => {
  it('scrolls directly to the given page of the centred chapter, without going through the reflow bracket', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB, chC], mountedChapters: [chA, chB] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 0)
    stubRect(container, 0)
    stubClientHeight(container, 100)
    stubPagesSequentially(wrapper, container, 300)

    container.scrollTop = 350 // mid = 400 → centres on ch-A page 1
    container.dispatchEvent(new Event('scroll'))
    expect(wrapper.emitted('centered')?.at(-1)).toEqual([{ chapterId: 'ch-A', page: 1 }])

    // contentTop adds the CURRENT scrollTop (350) to the stubbed rect.top, so
    // target a final scrollTop of 900 by stubbing 550 (900 - 350).
    stubRect(wrapper.find('[data-chapter-id="ch-A"][data-page="2"]').element, 550)
    // Synchronous: unlike applyScrollRequest (async, awaits nextTick),
    // seekToPage sets scrollTop directly — no reflow bracket to await.
    seekToPageOf(wrapper)(2)

    expect(container.scrollTop).toBe(900)
  })

  it('no-ops when nothing has been centred yet', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA], mountedChapters: [chA] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 42)

    seekToPageOf(wrapper)(1)

    expect(container.scrollTop).toBe(42) // untouched
  })
})

describe('ReaderStrip — chapter-finished', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('emits chapter-finished once per chapter even across repeated scrolls', () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB], mountedChapters: [chA] },
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
      props: { ...base, chapters: [chZ, chA], mountedChapters: [chZ] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 5000)
    stubRect(container, 0)
    stubRect(wrapper.find('[data-divider-id="ch-Z"]').element, -100) // "above", but 0 pages

    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200)

    expect(wrapper.emitted('chapter-finished')).toBeUndefined()
  })

  it('re-fires for the same chapter after a NEW scrollRequest token (a jump/resume is a fresh context)', async () => {
    const wrapper = mount(ReaderStrip, {
      props: { ...base, chapters: [chA, chB], mountedChapters: [chA] },
    })
    const container = wrapper.find('.strip').element as HTMLElement
    makeScrollable(container, 5000)
    stubRect(container, 0)
    stubRect(wrapper.find('[data-divider-id="ch-A"]').element, -100)

    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200)
    expect(wrapper.emitted('chapter-finished')).toHaveLength(1)

    await wrapper.setProps({ scrollRequest: { chapterId: 'ch-A', page: 0, token: 7 } })
    await flushPromises()

    container.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(200)

    expect(wrapper.emitted('chapter-finished')).toHaveLength(2)
  })
})
