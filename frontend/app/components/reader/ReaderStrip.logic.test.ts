/**
 * ReaderStrip.logic — the reader's pure decision math, tested without a browser.
 *
 * Covers the append/unmount window rule, the scroll-preservation delta, the
 * centered-page hit test, finished-chapter detection, and the pageCount tail-404
 * tolerance (the Slice-1 review requirement: trim only the CONTIGUOUS failed
 * tail; keep a real mid-chapter failure).
 */
import { describe, it, expect } from 'vitest'
import {
  MAX_MOUNTED,
  shouldAppend,
  chaptersToUnmount,
  scrollAfterUnmount,
  centeredPage,
  finishedChapterIds,
  trimTrailingFailures,
  type PageRect,
} from './ReaderStrip.logic'

describe('shouldAppend', () => {
  it('appends only when the sentinel is visible AND a next chapter exists', () => {
    expect(shouldAppend(true, true)).toBe(true)
    expect(shouldAppend(true, false)).toBe(false)
    expect(shouldAppend(false, true)).toBe(false)
  })
})

describe('chaptersToUnmount', () => {
  it('returns nothing while within the window cap', () => {
    expect(chaptersToUnmount([0], MAX_MOUNTED)).toEqual([])
    expect(chaptersToUnmount([0, 1, 2], MAX_MOUNTED)).toEqual([])
  })

  it('drops the far-above indices from the top when over the cap', () => {
    expect(chaptersToUnmount([0, 1, 2, 3], 3)).toEqual([0])
    expect(chaptersToUnmount([2, 3, 4, 5, 6], 3)).toEqual([2, 3])
  })
})

describe('scrollAfterUnmount', () => {
  it('subtracts the removed-above height so the position stays fixed', () => {
    // removed 400px of content above -> scrollTop drops by 400.
    expect(scrollAfterUnmount(1000, 3000, 2600)).toBe(600)
  })

  it('never goes negative', () => {
    expect(scrollAfterUnmount(100, 3000, 2600)).toBe(0)
  })
})

describe('centeredPage', () => {
  const pages: PageRect[] = [
    { chapterId: 'ch-a', page: 0, top: 0, bottom: 1000 },
    { chapterId: 'ch-a', page: 1, top: 1000, bottom: 2000 },
    { chapterId: 'ch-b', page: 0, top: 2100, bottom: 3100 }, // 100px gap = a divider
  ]

  it('returns null when nothing is mounted', () => {
    expect(centeredPage({ scrollTop: 0, viewportHeight: 800, pages: [] })).toBeNull()
  })

  it('returns the page containing the viewport midpoint', () => {
    // mid = 1200 + 400 = 1600 -> inside page 1 of ch-a.
    expect(centeredPage({ scrollTop: 1200, viewportHeight: 800, pages })).toEqual({ chapterId: 'ch-a', page: 1 })
  })

  it('falls back to the last page above the midpoint when it lands in a gap', () => {
    // mid = 1650 + 400 = 2050 -> in the 2000..2100 gap -> last page above is ch-a p1.
    expect(centeredPage({ scrollTop: 1650, viewportHeight: 800, pages })).toEqual({ chapterId: 'ch-a', page: 1 })
  })
})

describe('finishedChapterIds', () => {
  it('reports chapters whose end-divider is at or above the viewport top', () => {
    const dividers = [
      { chapterId: 'ch-a', top: 2000 },
      { chapterId: 'ch-b', top: 4200 },
    ]
    expect(finishedChapterIds(dividers, 2500)).toEqual(['ch-a'])
    expect(finishedChapterIds(dividers, 100)).toEqual([])
    expect(finishedChapterIds(dividers, 4200)).toEqual(['ch-a', 'ch-b'])
  })
})

describe('trimTrailingFailures', () => {
  it('keeps the full count when nothing failed', () => {
    expect(trimTrailingFailures(45, new Set())).toBe(45)
  })

  it('trims the contiguous failed tail (declared > real page count)', () => {
    // declared 50, real 45: pages 45..49 all 404.
    expect(trimTrailingFailures(50, new Set([45, 46, 47, 48, 49]))).toBe(45)
  })

  it('keeps a real mid-chapter failure (a non-failed page exists after it)', () => {
    // page 10 failed but 11..14 fine -> not a tail, keep all 15 (10 shows placeholder).
    expect(trimTrailingFailures(15, new Set([10]))).toBe(15)
  })

  it('stops trimming at the first non-failed page from the end', () => {
    // 48 fine, 49 failed -> only trims 49.
    expect(trimTrailingFailures(50, new Set([49]))).toBe(49)
  })
})
