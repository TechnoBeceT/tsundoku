/**
 * ReaderStrip.logic — the PURE decision math behind the long-strip reader.
 *
 * The DOM-bound wiring (IntersectionObserver, scroll listeners, element refs)
 * lives in ReaderStrip.vue; every decision it makes is delegated to one of the
 * pure functions here so they can be unit-tested without a browser. Nothing in
 * this file touches the DOM or Vue reactivity — inputs and outputs are plain
 * values.
 *
 * Shared with `useReader` (§2 DRY): `chaptersToUnmountDirectional` is the single
 * window-bounding rule used both by the strip's append/prepend handlers and by
 * the composable's `onNearTail`/`onNearHead`, so "how big is the mounted window"
 * is defined in exactly one place.
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
 * finishedChapterIds — the chapters the reader has actually SCROLLED DOWN
 * THROUGH, i.e. "finished" is a TRANSITION, not a static divider position.
 *
 * A chapter is finished only when its end-divider was previously observed
 * BELOW the scroll position (`seenBelow`) and is now at/above it — the reader
 * moved past it. This matters because a HEAD PREPEND inserts a whole chapter
 * entirely above the current scroll position: its divider sits at/above
 * `scrollTop` on the very FIRST observation, with no prior "seen below" —
 * a naive static check (`top <= scrollTop`) would fire instantly, marking the
 * chapter the reader is about to read as fully read and destroying its resume
 * position (the CRITICAL bug this fixes). Requiring the below→above
 * transition means a never-seen divider that starts at/above `scrollTop`
 * (exactly the prepend case) never fires — only once the reader scrolls UP
 * into that chapter (seeing its divider below them) and back DOWN through it
 * does it correctly finish.
 *
 * `dividerTops` is each mounted chapter's end-divider top offset from the
 * scroll container. `seenBelow` is the running set of chapter ids whose
 * divider has been observed below the reader; pass the returned `seenBelow`
 * back in on the next call (the strip owns this as persistent per-instance
 * state, like its `emittedFinished` de-dupe set — see that field's doc for why
 * a SEPARATE de-dupe still sits on top of this: this function does not track
 * "already emitted", only "has crossed the line", so calling it twice with the
 * same at/above divider legitimately returns the same id both times).
 */
export function finishedChapterIds(
  dividerTops: { chapterId: string, top: number }[],
  scrollTop: number,
  seenBelow: Set<string>,
): { finished: string[], seenBelow: Set<string> } {
  const nextSeenBelow = new Set(seenBelow)
  const finished: string[] = []
  for (const d of dividerTops) {
    if (d.top > scrollTop) {
      nextSeenBelow.add(d.chapterId)
    }
    else if (nextSeenBelow.has(d.chapterId)) {
      finished.push(d.chapterId)
    }
  }
  return { finished, seenBelow: nextSeenBelow }
}

/**
 * pruneSeenBelow — drops observations for chapters that are no longer mounted.
 * `seenBelow` (see `finishedChapterIds`) is an observation about the CURRENT
 * mounted window: "this chapter's divider has been seen below the reader." Once
 * a chapter is unmounted (the window slid forward or backward past it), that
 * observation is stale — and dangerous if left in place: a chapter jump
 * (`useReader.jumpToChapter`) collapses the window to a different chapter and
 * mints a fresh `scrollRequest` token, which clears the strip's `emittedFinished`
 * de-dupe set but NOT `seenBelow`. If the reader then scrolls up and the
 * abandoned chapter is PREPENDED back into the window, its divider lands
 * above `scrollTop` on this fresh mount — and a stale `seenBelow` entry would
 * make `finishedChapterIds` read that as a below->above transition, firing
 * `chapter-finished` for a chapter the reader never actually read this pass
 * (the same class of bug `finishedChapterIds` itself was built to prevent for
 * the plain-prepend case). Pruning by the mounted window on every window change
 * closes this without touching the token-based `emittedFinished` clear — it
 * also fixes the scroll-only path (unmount without any jump), where no token
 * is ever minted.
 */
export function pruneSeenBelow(seenBelow: Set<string>, mountedIds: string[]): Set<string> {
  const mounted = new Set(mountedIds)
  const pruned = new Set<string>()
  for (const id of seenBelow) {
    if (mounted.has(id)) pruned.add(id)
  }
  return pruned
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

/** Which end of the mounted window a reflow is growing towards. */
export type WindowDirection = 'forward' | 'backward'

/**
 * chaptersToUnmountDirectional — the window-bounding rule, aware of which way the
 * reader is moving. Keeps at most `maxMounted` chapters mounted and drops from the
 * end the reader is moving AWAY from: scrolling forward drops the chapters far
 * ABOVE, scrolling backward drops the chapters far BELOW.
 *
 * The direction matters: the original rule always sliced from the top, which — once
 * a head-prepend exists — would unmount the very chapter just prepended, and the
 * window would fight the reader instead of sliding with them.
 *
 * Returns `[]` when already within bounds.
 */
export function chaptersToUnmountDirectional(
  mounted: number[],
  maxMounted: number,
  direction: WindowDirection,
): number[] {
  if (mounted.length <= maxMounted) return []
  const excess = mounted.length - maxMounted
  return direction === 'forward' ? mounted.slice(0, excess) : mounted.slice(mounted.length - excess)
}

/**
 * shouldPrepend — whether the strip should pull the PREVIOUS chapter into the
 * window. True only when the head sentinel is on screen, a previous chapter
 * exists, AND the reader is currently centred on the FIRST MOUNTED chapter.
 *
 * The third condition (added for Fix 2+3) is what makes a prepend safe and
 * meaningful: a prepend is only useful once the reader has scrolled to the top
 * of what's mounted and is approaching it, and gating on it structurally
 * guarantees (a) `centeredChapterId` is non-null whenever a prepend fires, so
 * the reflow anchor never falls back to the tail chapter — the very element a
 * BACKWARD reflow can unmount; (b) the backward window-drop (which trims from
 * the BOTTOM) can never remove the chapter the reader is centred on, since
 * that chapter is always the first, never the last, of the mounted window;
 * and (c) no spurious prepend fires at mount, before anything has been
 * centred yet.
 */
export function shouldPrepend(sentinelVisible: boolean, hasPrevChapter: boolean, isCentredOnFirstMounted: boolean): boolean {
  return sentinelVisible && hasPrevChapter && isCentredOnFirstMounted
}
