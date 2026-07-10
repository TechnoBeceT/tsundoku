/**
 * ReaderStrip.logic — the PURE decision math behind the long-strip reader.
 *
 * The DOM-bound wiring (IntersectionObserver, scroll listeners, element refs)
 * lives in ReaderStrip.vue; every decision it makes is delegated to one of the
 * pure functions here so they can be unit-tested without a browser. Nothing in
 * this file touches the DOM or Vue reactivity — inputs and outputs are plain
 * values.
 *
 * Shared with `useReader` (§2 DRY): `chaptersToUnmount` is the single window-
 * bounding rule used both by the strip's append handler and by the composable's
 * `onNearTail`, so "how big is the mounted window" is defined in exactly one place.
 */

/** The number of chapters kept mounted at once — the current chapter plus a
 *  one-chapter buffer on each side ("current ±1"). Bounds the live DOM node
 *  count so a long binge never accumulates every chapter's pages in memory. */
export const MAX_MOUNTED = 3

/**
 * shouldAppend — whether the strip should pull the next chapter into the window.
 * True only when the tail sentinel is on screen AND a next chapter actually
 * exists; the strip calls `useReader.onNearTail()` when this returns true.
 */
export function shouldAppend(sentinelVisible: boolean, hasNextChapter: boolean): boolean {
  return sentinelVisible && hasNextChapter
}

/**
 * chaptersToUnmount — given the currently mounted chapter indices (ascending)
 * and the window cap, returns the far-ABOVE indices that must be unmounted to
 * keep the window at most `maxMounted` chapters. Always drops from the TOP of
 * the list (the chapters furthest above the reader's position), never the tail
 * the reader is actively approaching. Returns `[]` when already within bounds.
 */
export function chaptersToUnmount(mounted: number[], maxMounted: number): number[] {
  if (mounted.length <= maxMounted) return []
  return mounted.slice(0, mounted.length - maxMounted)
}

/**
 * scrollAfterReflow — the scrollTop that keeps the reading position visually
 * fixed after the mounted window reflows. Because one `onNearTail` both appends
 * below AND may unmount a far-above chapter, the total scrollHeight delta is not
 * the amount the viewport shifted — so this anchors on a RETAINED element: given
 * the anchor's content-relative top before and after the reflow, shift scrollTop
 * by the same delta so the anchor stays under the same viewport point. Unmounting
 * above moves the anchor up (newTop < prevTop) and scrollTop drops to match; the
 * seam never visibly jumps. Never negative.
 */
export function scrollAfterReflow(prevScrollTop: number, prevAnchorTop: number, newAnchorTop: number): number {
  return Math.max(0, prevScrollTop + (newAnchorTop - prevAnchorTop))
}

/** A single rendered page's vertical extent within the scroll container, tagged
 *  with the chapter + 0-based page index it represents. */
export interface PageRect {
  /** Chapter UUID this page belongs to. */
  chapterId: string
  /** 0-based page index within the chapter. */
  page: number
  /** Page top offset from the scroll container's top (px). */
  top: number
  /** Page bottom offset from the scroll container's top (px). */
  bottom: number
}

/** The scroll snapshot `centeredPage` reasons over — the viewport window plus
 *  every mounted page's extent. */
export interface ScrollState {
  /** Current scroll offset of the container (px). */
  scrollTop: number
  /** Visible viewport height (px). */
  viewportHeight: number
  /** Every mounted page's extent, in document order. */
  pages: PageRect[]
}

/** The chapter + page currently under the viewport's vertical midpoint. */
export interface CenteredPage {
  /** Chapter UUID under the viewport midpoint. */
  chapterId: string
  /** 0-based page index under the viewport midpoint. */
  page: number
}

/**
 * centeredPage — the page sitting under the viewport's vertical midpoint, which
 * the strip emits as the reader's live position (Slice 3 persists it). Returns
 * the page whose extent contains the midpoint; if the midpoint falls in a gap
 * (e.g. over a divider) it returns the last page above the midpoint, else the
 * first page. Returns null only when nothing is mounted.
 */
export function centeredPage(state: ScrollState): CenteredPage | null {
  if (state.pages.length === 0) return null
  const mid = state.scrollTop + state.viewportHeight / 2
  let lastAbove: PageRect | null = null
  for (const rect of state.pages) {
    if (mid >= rect.top && mid < rect.bottom) return { chapterId: rect.chapterId, page: rect.page }
    if (rect.bottom <= mid) lastAbove = rect
  }
  const pick = lastAbove ?? state.pages[0]!
  return { chapterId: pick.chapterId, page: pick.page }
}

/**
 * finishedChapterIds — the chapters whose end-divider has scrolled fully above
 * the viewport top, i.e. the reader has finished reading them. `dividerTops` is
 * each chapter's end-divider top offset from the scroll container; a divider at
 * or above `scrollTop` means that chapter is behind the reader. The strip diffs
 * this against an already-emitted set so `chapterFinished` fires once per chapter.
 */
export function finishedChapterIds(dividerTops: { chapterId: string, top: number }[], scrollTop: number): string[] {
  return dividerTops.filter((d) => d.top <= scrollTop).map((d) => d.chapterId)
}

/**
 * trimTrailingFailures — the number of pages the strip should actually show for
 * a chapter, given its DECLARED `pageCount` and the set of page indices that
 * failed to load. A chapter's declared count (from ComicInfo / download) may
 * exceed the CBZ's real image count, so the trailing pages 404. This trims the
 * CONTIGUOUS failed tail — pages at the very end with nothing rendered after
 * them — so the reader treats them as end-of-chapter and advances. A failure in
 * the MIDDLE (any non-failed page after it) is a real page error and is KEPT
 * (it renders the "page unavailable" placeholder), so trimming stops at the
 * first non-failed page from the end. Never trims below 0.
 */
export function trimTrailingFailures(pageCount: number, failed: Set<number>): number {
  let visible = pageCount
  for (let i = pageCount - 1; i >= 0; i--) {
    if (!failed.has(i)) break
    visible = i
  }
  return visible
}
