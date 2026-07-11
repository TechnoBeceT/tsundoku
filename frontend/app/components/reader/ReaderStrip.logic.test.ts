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
  chaptersToUnmountDirectional,
  shouldPrepend,
  scrollAfterReflow,
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

describe('scrollAfterReflow', () => {
  it('shifts scrollTop by the anchor delta so the read position stays fixed', () => {
    // the retained anchor moved up 400px (unmount above) -> scrollTop drops 400.
    expect(scrollAfterReflow(1000, 2000, 1600)).toBe(600)
  })

  it('leaves scrollTop unchanged when the anchor did not move (pure append below)', () => {
    expect(scrollAfterReflow(1000, 2000, 2000)).toBe(1000)
  })

  it('never goes negative', () => {
    expect(scrollAfterReflow(100, 2000, 1600)).toBe(0)
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
  it('does NOT finish a divider that is at/above scrollTop but was never seen below (the head-prepend case)', () => {
    // A prepended chapter's end-divider sits ABOVE the current scroll position
    // on the very FIRST observation — this is the reviewer's exact CRITICAL
    // repro (Fix 1): a static "top <= scrollTop" check would fire instantly.
    const dividers = [{ chapterId: 'ch-prev', top: 100 }]
    const { finished, seenBelow } = finishedChapterIds(dividers, 500, new Set())
    expect(finished).toEqual([])
    // Not finished yet, but now tracked as "at/above" — it still has not been
    // seen BELOW the reader, so it stays un-finishable until they scroll up
    // into it first.
    expect(seenBelow.has('ch-prev')).toBe(false)
  })

  it('finishes a chapter only once its divider transitions from below the reader to at/above (a real scroll-through)', () => {
    const dividers = [{ chapterId: 'ch-a', top: 2000 }]

    // Pass 1: divider is still BELOW scrollTop — seeds seenBelow, not finished.
    const pass1 = finishedChapterIds(dividers, 1000, new Set())
    expect(pass1.finished).toEqual([])
    expect(pass1.seenBelow.has('ch-a')).toBe(true)

    // Pass 2: the reader scrolled down past it — divider now at/above scrollTop
    // AND it was seen below in pass 1 -> finished.
    const pass2 = finishedChapterIds(dividers, 2500, pass1.seenBelow)
    expect(pass2.finished).toEqual(['ch-a'])
  })

  it('reports multiple chapters independently by the same below->above rule', () => {
    const dividers = [
      { chapterId: 'ch-a', top: 2000 },
      { chapterId: 'ch-b', top: 4200 },
    ]
    // Both start out below scrollTop=100 -> neither finished, both now seenBelow.
    const seeded = finishedChapterIds(dividers, 100, new Set()).seenBelow
    expect(finishedChapterIds(dividers, 100, seeded).finished).toEqual([])
    // Scrolling to 2500 crosses only ch-a's divider.
    expect(finishedChapterIds(dividers, 2500, seeded).finished).toEqual(['ch-a'])
    // Scrolling to 4200 crosses both.
    expect(finishedChapterIds(dividers, 4200, seeded).finished).toEqual(['ch-a', 'ch-b'])
  })

  it('does not itself de-dupe repeated calls — a chapter already at/above and already seenBelow reports finished every call (the strip layers emittedFinished on top for the once-per-session guarantee)', () => {
    const dividers = [{ chapterId: 'ch-a', top: 2000 }]
    const seeded = new Set(['ch-a']) // already observed below in a prior pass

    // Scrolling back UP above the divider, then back DOWN through it again: the
    // pure function reports "finished" both times it's crossed — de-dupe is
    // explicitly the CALLER's job (the strip's `emittedFinished` Set), not this
    // function's. This is the contract §1's "scroll up then down again" case
    // exercises: the strip must not double-emit even though this function
    // would report `finished` on both down-crossings.
    // scrollTop=100 puts the divider (top=2000) back BELOW the reader — as if
    // they scrolled back up above it.
    const up = finishedChapterIds(dividers, 100, seeded)
    expect(up.finished).toEqual([])
    const down = finishedChapterIds(dividers, 2500, up.seenBelow)
    expect(down.finished).toEqual(['ch-a'])
    const downAgain = finishedChapterIds(dividers, 2600, down.seenBelow)
    expect(downAgain.finished).toEqual(['ch-a']) // reports again — caller must de-dupe
  })

  it('PROOF: the OLD always-fire rule (top <= scrollTop, no history) would have wrongly finished a prepended chapter — this test would FAIL under it', () => {
    // Reproduces the OLD single-arg contract inline so the contrast is explicit
    // and mechanically checked, not just asserted in prose.
    const oldRuleFinished = (dividerTops: { chapterId: string, top: number }[], scrollTop: number): string[] =>
      dividerTops.filter((d) => d.top <= scrollTop).map((d) => d.chapterId)

    const prepended = [{ chapterId: 'ch-prev', top: 100 }]
    const scrollTop = 500 // reader is still reading the CURRENT chapter; ch-prev was just prepended above them

    // OLD rule: instant false-positive "finished" the moment the chapter mounts.
    expect(oldRuleFinished(prepended, scrollTop)).toEqual(['ch-prev'])
    // NEW rule: correctly withheld until the reader actually scrolls DOWN
    // through ch-prev (seenBelow first). Asserting `[]` here is exactly the
    // assertion that fails under the old rule above.
    expect(finishedChapterIds(prepended, scrollTop, new Set()).finished).toEqual([])
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

describe('chaptersToUnmountDirectional', () => {
  it('drops from the TOP when moving forward', () => {
    expect(chaptersToUnmountDirectional([0, 1, 2, 3], 3, 'forward')).toEqual([0])
  })

  it('drops from the BOTTOM when moving backward', () => {
    expect(chaptersToUnmountDirectional([0, 1, 2, 3], 3, 'backward')).toEqual([3])
  })

  it('never unmounts the chapter just prepended', () => {
    // Moving backward, index 0 was just prepended — it must survive.
    expect(chaptersToUnmountDirectional([0, 1, 2, 3], 3, 'backward')).not.toContain(0)
  })

  it('returns [] when already within bounds', () => {
    expect(chaptersToUnmountDirectional([1, 2], 3, 'forward')).toEqual([])
    expect(chaptersToUnmountDirectional([1, 2], 3, 'backward')).toEqual([])
  })
})

describe('shouldPrepend', () => {
  it('is true only when the head sentinel is visible AND a previous chapter exists AND the reader is centred on the first mounted chapter', () => {
    expect(shouldPrepend(true, true, true)).toBe(true)
    expect(shouldPrepend(true, false, true)).toBe(false)
    expect(shouldPrepend(false, true, true)).toBe(false)
  })

  it('is false when centred on a LATER mounted chapter, even with the sentinel visible and a prev chapter available (Fix 2+3)', () => {
    // This is the scenario that used to unmount the centred chapter: the head
    // sentinel intersecting while the reader is centred on a chapter other
    // than the first mounted one is no longer sufficient to prepend.
    expect(shouldPrepend(true, true, false)).toBe(false)
  })
})
