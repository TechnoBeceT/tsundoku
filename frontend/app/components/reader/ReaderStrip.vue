<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import ReaderPage from './ReaderPage.vue'
import ChapterDivider from './ChapterDivider.vue'
import type { ReaderChapter, ScrollRequest } from '~/composables/useReader'
import {
  shouldAppend,
  shouldPrepend,
  centeredPage,
  finishedChapterIds,
  trimTrailingFailures,
  scrollAfterReflow,
  type PageRect,
} from './ReaderStrip.logic'

/**
 * ReaderStrip — the vertical long-strip reader core. Renders the mounted window
 * of chapters (from `useReader`) as stacked `ReaderPage`s separated by
 * `ChapterDivider`s, and drives the infinite-scroll behaviour in BOTH directions:
 *
 *   - A TAIL sentinel appends the next chapter early (emits `near-tail`); a HEAD
 *     sentinel prepends the previous chapter early (emits `near-head`). Both wire
 *     to `useReader.onNearTail`/`onNearHead`, which grow the window and unmount
 *     whichever far end the reader is moving away from.
 *   - Every reflow that can move content ABOVE the viewport — a prepend, an
 *     append-driven unmount, or a pageCount tail-404 trim — is bracketed by
 *     `beforeReflow`/`afterReflow`, which anchors on the CENTRED chapter (see its
 *     doc comment for why the tail is no longer a safe anchor) so the seam never
 *     visibly jumps.
 *   - A throttled scroll handler emits `centered` (the page under the viewport
 *     midpoint), `visible-pages` (that chapter's trimmed page count — the
 *     slider's live denominator) and `chapter-finished` (when a chapter's
 *     end-divider scrolls above the viewport top).
 *   - `scrollRequest` (a token-tagged scroll-to-target, e.g. the route's resume
 *     anchor or a chapter jump) is honoured once per distinct token; `seekToPage`
 *     is exposed for the page slider to scroll within the centred chapter
 *     directly, WITHOUT going through the reflow-anchor bracket (a seek sets the
 *     position, it doesn't need to survive a DOM change around it).
 *   - Applies the pageCount tail-404 tolerance: a page that fails to load and
 *     forms the contiguous end of a chapter is trimmed (declared count may
 *     exceed the real CBZ) so the reader advances; a mid-chapter failure keeps
 *     its placeholder (`trimTrailingFailures`).
 *
 * The pure decisions live in ReaderStrip.logic.ts (unit-tested); this SFC only
 * wires them to the DOM. The column width / side padding / page-gap look is
 * driven by the reader settings via inherited CSS custom properties (see the
 * `<style>` note), each defaulting to the flush Slice-2 layout when unset.
 */
const props = defineProps<{
  /** The full downloaded chapter list (for next-chapter labels + hasNext). */
  chapters: ReaderChapter[]
  /** The chapters currently mounted (the window `useReader` maintains). */
  mountedChapters: ReaderChapter[]
  /** Builds the same-origin page-bytes URL for (chapterId, 0-based page). */
  pageUrl: (chapterId: string, n: number) => string
  /** Whether a chapter precedes the centred one (`useReader.hasPrev`) — gates
   *  the head sentinel's prepend, the mirror of the tail's local `hasNext`. */
  hasPrev: boolean
  /** The strip's pending scroll-to-target instruction — the route's resume
   *  anchor on open, or a later chapter jump (`useReader.scrollRequest`). Acted
   *  on once per distinct `token` (see the watcher below); null when nothing is
   *  pending. */
  scrollRequest: ScrollRequest | null
}>()

const emit = defineEmits<{
  /** The tail sentinel appeared — append the next chapter. */
  'near-tail': []
  /** The head sentinel appeared — prepend the previous chapter. */
  'near-head': []
  /** The page under the viewport midpoint changed (throttled). */
  'centered': [payload: { chapterId: string, page: number }]
  /** A chapter's end-divider scrolled above the viewport top. */
  'chapter-finished': [chapterId: string]
  /** The centred chapter's TRIMMED page count changed — the slider's live
   *  denominator. Never the DECLARED `pageCount`, which may exceed the CBZ's
   *  real image count (the pageCount tail-404 tolerance). */
  'visible-pages': [payload: { chapterId: string, count: number }]
}>()

const scrollEl = ref<HTMLElement | null>(null)
const headSentinelEl = ref<HTMLElement | null>(null)
const sentinelEl = ref<HTMLElement | null>(null)

// Per-chapter failed page indices (0-based). Reassigned (not mutated) so the
// `visiblePages` computed re-runs — a page whose <img> errors is dropped from the
// end of its chapter when it forms the contiguous tail (declared > real count).
const pageFailures = ref<Record<string, Set<number>>>({})

/** The chapter that follows `chapter` in the full list, or undefined at the end. */
function nextChapterOf(chapter: ReaderChapter): ReaderChapter | undefined {
  const idx = props.chapters.findIndex((c) => c.id === chapter.id)
  return idx >= 0 ? props.chapters[idx + 1] : undefined
}

/** The divider's "finished" ref for a mounted chapter. */
function finishedRef(chapter: ReaderChapter): { number: number | null, name: string } {
  return { number: chapter.number, name: chapter.name }
}

/** The divider's "next" ref (undefined at the last chapter → end message). */
function nextRef(chapter: ReaderChapter): { number: number | null, name: string } | undefined {
  const next = nextChapterOf(chapter)
  return next ? { number: next.number, name: next.name } : undefined
}

/** Visible page count for a chapter after trimming its contiguous failed tail. */
function visiblePages(chapter: ReaderChapter): number {
  return trimTrailingFailures(chapter.pageCount, pageFailures.value[chapter.id] ?? new Set())
}

/** Records a page load failure so the tail-404 tolerance can trim it. A trailing
 *  failure shrinks the chapter's rendered pages; bracket it in the anchor reflow
 *  so trimming height ABOVE the viewport does not jump the read position (Fix A).
 *  A mid-chapter failure does not change `visiblePages`, so the bracket is a no-op. */
function onPageError(chapterId: string, page: number): void {
  beforeReflow()
  const set = new Set(pageFailures.value[chapterId] ?? [])
  set.add(page)
  pageFailures.value = { ...pageFailures.value, [chapterId]: set }
  void afterReflow()
}

/** True while the last mounted chapter is not the last in the full list. */
const hasNext = computed(() => {
  const lastId = props.mountedChapters.at(-1)?.id
  const idx = props.chapters.findIndex((c) => c.id === lastId)
  return idx >= 0 && idx < props.chapters.length - 1
})

// ---- live reading position: the CENTRED chapter/page ------------------------
// Tracked from `runScroll`'s `centeredPage` result — used to pick the reflow
// anchor (see `beforeReflow`), the `seekToPage`/`pageDistance` target, and the
// `visible-pages` chapter.
const centeredChapterId = ref<string | null>(null)
const centeredPageIndex = ref<number | null>(null)

/** Distance (in pages) of (chapterId, page) from the CENTRED page — biases
 *  eager preloading toward the pages nearest the reader's live position (a
 *  later slice consumes this on `ReaderPage`). Pages outside the centred
 *  chapter are simply "far" — there is no cross-chapter page axis to compare. */
function pageDistance(chapterId: string, page: number): number {
  if (chapterId !== centeredChapterId.value || centeredPageIndex.value == null) return Infinity
  return Math.abs(page - centeredPageIndex.value)
}

// ---- window reflow: preserve the read position (anchor on the centred chapter) ---
// Used for the append/prepend (unmount-from-either-end) paths AND the pageCount
// tail-404 trim path: any DOM change that can remove height ABOVE the viewport is
// bracketed by beforeReflow → afterReflow so the viewed position never jumps.
// `reflowPending` coalesces overlapping brackets in one tick (e.g. several
// trailing pages 404 at once) to a single snapshot→restore, so the earliest
// pre-change snapshot wins.
let anchorId: string | null = null
let anchorPrevTop = 0
let prevScrollTop = 0
let reflowPending = false

/** Content-relative top of an element inside the scroll container (getBoundingClientRect based, offsetParent-agnostic). */
function contentTop(el: HTMLElement, container: HTMLElement): number {
  return el.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop
}

/**
 * Snapshot the retained anchor chapter's position just before a reflow (once per
 * tick). Anchors on the CENTRED chapter, NOT the tail. Before the head sentinel
 * existed a reflow could only ever unmount from the TOP, so the tail chapter was
 * always retained and safe to anchor on — that assumption is now FALSE: a
 * BACKWARD reflow (a head prepend) unmounts from the BOTTOM, so the tail can be
 * the very element that disappears. When that happens `afterReflow()` finds no
 * anchor element, returns early, and scrollTop is never corrected — the page
 * visibly jumps under the reader's thumb. The window always slides AROUND the
 * chapter being read, so the centred chapter is retained by construction
 * regardless of which end drops. Falls back to the tail only before anything has
 * been centred yet (the very first paint, pre-scroll).
 */
function beforeReflow(): void {
  if (reflowPending) return
  const el = scrollEl.value
  if (!el) return
  anchorId = centeredChapterId.value ?? props.mountedChapters.at(-1)?.id ?? null
  const anchorEl = anchorId ? el.querySelector<HTMLElement>(`[data-chapter-id="${anchorId}"]`) : null
  anchorPrevTop = anchorEl ? contentTop(anchorEl, el) : 0
  prevScrollTop = el.scrollTop
  reflowPending = true
}

/** After the reflow paints, shift scrollTop so the anchor stays put. */
async function afterReflow(): Promise<void> {
  await nextTick()
  if (!reflowPending) return
  reflowPending = false
  const el = scrollEl.value
  if (!el || !anchorId) return
  const anchorEl = el.querySelector<HTMLElement>(`[data-chapter-id="${anchorId}"]`)
  if (!anchorEl) return
  el.scrollTop = scrollAfterReflow(prevScrollTop, anchorPrevTop, contentTop(anchorEl, el))
}

// De-duped `chapter-finished` ids, per strip instance (each mount owns its own
// Set — it is NOT module-scoped). A chapter can be RE-entered by scrolling back
// up now that backward scrolling is legal, and re-entering must not re-fire
// `chapter-finished` — but a jump/resume IS a fresh context, so the watcher
// below clears this Set whenever a new `scrollRequest` token arrives.
const emittedFinished = new Set<string>()

// ---- scroll-to-target: honour `scrollRequest` --------------------------------
// Replaces the old one-shot `didInitialScroll` boolean fuse, which could only
// ever fire once for the strip's whole lifetime — silently no-op-ing every
// chapter jump after the first. A monotonic token lets EVERY new request (the
// route's initial resume anchor, then any later jump) scroll again, while a
// stale/unchanged token (a re-render carrying the same request) is skipped.
let lastScrollRequestToken = 0

/**
 * applyScrollRequest — scrolls the container to the requested (chapterId, page),
 * falling back to the chapter's top when the specific page element isn't mounted
 * yet. Deliberately NOT reflow-bracketed: a scroll-to-target request is SETTING
 * the position, not preserving one across an unrelated DOM change — running it
 * through `beforeReflow`/`afterReflow` would fight the very scroll it performs.
 */
async function applyScrollRequest(target: ScrollRequest): Promise<void> {
  const el = scrollEl.value
  if (!el) return
  await nextTick()
  const pageEl = el.querySelector<HTMLElement>(`[data-chapter-id="${target.chapterId}"][data-page="${target.page}"]`)
  const chapterEl = el.querySelector<HTMLElement>(`[data-chapter-id="${target.chapterId}"]`)
  const anchor = pageEl ?? chapterEl
  if (!anchor) return
  el.scrollTop = contentTop(anchor, el)
}

watch(() => props.scrollRequest, (req) => {
  if (!req || req.token === lastScrollRequestToken) return
  lastScrollRequestToken = req.token
  // A jump/resume is a fresh reading context — a chapter finished in a PREVIOUS
  // pass through this strip must be able to fire `chapter-finished` again if the
  // reader lands back on it (e.g. jumping to an earlier chapter).
  emittedFinished.clear()
  void applyScrollRequest(req)
})

/**
 * seekToPage — scrolls to the given 0-based page of the CENTRED chapter (the
 * page slider's target). Deliberately bypasses `beforeReflow`/`afterReflow` —
 * see `applyScrollRequest`'s doc comment; the same reasoning applies here.
 */
function seekToPage(page: number): void {
  const el = scrollEl.value
  const chapterId = centeredChapterId.value
  if (!el || !chapterId) return
  const pageEl = el.querySelector<HTMLElement>(`[data-chapter-id="${chapterId}"][data-page="${page}"]`)
  const chapterEl = el.querySelector<HTMLElement>(`[data-chapter-id="${chapterId}"]`)
  const anchor = pageEl ?? chapterEl
  if (!anchor) return
  el.scrollTop = contentTop(anchor, el)
}

defineExpose({ seekToPage })

// ---- sentinels: append the next chapter / prepend the previous one ----------
let observer: IntersectionObserver | null = null

/** Routes an IntersectionObserver callback to the head or tail sentinel handler
 *  by comparing `entry.target` — both sentinels share one observer instance. */
function onIntersect(entries: IntersectionObserverEntry[]): void {
  for (const entry of entries) {
    if (!entry.isIntersecting) continue
    if (entry.target === headSentinelEl.value) {
      if (!shouldPrepend(true, props.hasPrev)) continue
      // MANDATORY bracket: an un-anchored prepend inserts a whole chapter ABOVE
      // the scroll position and yanks the page down under the reader's thumb.
      beforeReflow()
      emit('near-head')
      void afterReflow()
    }
    else if (entry.target === sentinelEl.value) {
      if (!shouldAppend(true, hasNext.value)) continue
      beforeReflow()
      emit('near-tail')
      void afterReflow()
    }
  }
}

// ---- scroll: emit centered + visible-pages + chapter-finished (throttled) ---
const THROTTLE_MS = 120
let lastRun = 0
let pendingTimer: ReturnType<typeof setTimeout> | null = null
let lastVisiblePages: { chapterId: string, count: number } | null = null

function runScroll(): void {
  const el = scrollEl.value
  if (!el) return
  const containerTop = el.getBoundingClientRect().top

  const pages: PageRect[] = []
  el.querySelectorAll<HTMLElement>('[data-page]').forEach((pageEl) => {
    const r = pageEl.getBoundingClientRect()
    const top = r.top - containerTop + el.scrollTop
    pages.push({
      chapterId: pageEl.dataset.chapterId ?? '',
      page: Number(pageEl.dataset.page),
      top,
      bottom: top + r.height,
    })
  })

  const centered = centeredPage({ scrollTop: el.scrollTop, viewportHeight: el.clientHeight, pages })
  if (centered) {
    centeredChapterId.value = centered.chapterId
    centeredPageIndex.value = centered.page
    emit('centered', centered)

    const chapter = props.mountedChapters.find((c) => c.id === centered.chapterId)
    if (chapter) {
      const count = visiblePages(chapter)
      if (lastVisiblePages?.chapterId !== centered.chapterId || lastVisiblePages.count !== count) {
        lastVisiblePages = { chapterId: centered.chapterId, count }
        emit('visible-pages', { chapterId: centered.chapterId, count })
      }
    }
  }

  const dividerTops: { chapterId: string, top: number }[] = []
  el.querySelectorAll<HTMLElement>('[data-divider-id]').forEach((divEl) => {
    const cid = divEl.dataset.dividerId ?? ''
    // Fix D: a chapter with zero visible pages (all failed, or an imported
    // ComicInfo PageCount of 0) has its end-divider at ~top 0, which would
    // false-fire "finished" on the very first scroll before anything is read.
    // Skip such chapters — there is nothing to finish.
    const chapter = props.mountedChapters.find((c) => c.id === cid)
    if (!chapter || visiblePages(chapter) === 0) return
    dividerTops.push({
      chapterId: cid,
      top: divEl.getBoundingClientRect().top - containerTop + el.scrollTop,
    })
  })
  for (const id of finishedChapterIds(dividerTops, el.scrollTop)) {
    if (!emittedFinished.has(id)) {
      emittedFinished.add(id)
      emit('chapter-finished', id)
    }
  }
}

/** Trailing-edge throttle so scroll spam collapses into ~1 emit per THROTTLE_MS. */
function onScroll(): void {
  const now = Date.now()
  const remaining = THROTTLE_MS - (now - lastRun)
  if (remaining <= 0) {
    lastRun = now
    runScroll()
    return
  }
  // Schedule a single trailing run; the guard is the ??= (only sets when idle).
  pendingTimer ??= setTimeout(() => {
    pendingTimer = null
    lastRun = Date.now()
    runScroll()
  }, remaining)
}

onMounted(() => {
  const el = scrollEl.value
  if (el) {
    // rootMargin prefetches the next/previous chapter ~a viewport early so
    // neither seam is noticeable. One observer watches both sentinels.
    observer = new IntersectionObserver(onIntersect, { root: el, rootMargin: '600px 0px' })
    if (headSentinelEl.value) observer.observe(headSentinelEl.value)
    if (sentinelEl.value) observer.observe(sentinelEl.value)
  }
  const req = props.scrollRequest
  if (req && req.token !== lastScrollRequestToken) {
    lastScrollRequestToken = req.token
    void applyScrollRequest(req)
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
  if (pendingTimer) clearTimeout(pendingTimer)
})
</script>

<template>
  <div ref="scrollEl" class="strip" @scroll="onScroll">
    <div class="strip__col">
      <div ref="headSentinelEl" class="strip__sentinel" data-sentinel="head" aria-hidden="true" />
      <template v-for="chapter in mountedChapters" :key="chapter.id">
        <div class="strip__chapter" :data-chapter-id="chapter.id">
          <ReaderPage
            v-for="n in visiblePages(chapter)"
            :key="n"
            :data-chapter-id="chapter.id"
            :data-page="n - 1"
            :src="pageUrl(chapter.id, n - 1)"
            :alt="`Page ${n}`"
            :distance-from-centre="pageDistance(chapter.id, n - 1)"
            @error="onPageError(chapter.id, n - 1)"
          />
        </div>
        <ChapterDivider
          :data-divider-id="chapter.id"
          :finished="finishedRef(chapter)"
          :next="nextRef(chapter)"
        />
      </template>
      <div ref="sentinelEl" class="strip__sentinel" data-sentinel="tail" aria-hidden="true" />
    </div>
  </div>
</template>

<style scoped>
/* The column width, side padding, and inter-page gap are driven by the reader
   settings via CSS custom properties (useReaderSettings.readerStyleVars, set on
   the route's `.reader` container and inherited here). Each falls back to the
   Slice-2/3 default when unset, so the strip is unchanged with no settings. */
.strip {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--bg);
  -webkit-overflow-scrolling: touch;
  overscroll-behavior: contain;
}

.strip__col {
  max-width: var(--reader-col-max, 800px);
  margin: 0 auto;
  padding-inline: var(--reader-side-pad, 0);
}

/* Flex column so `--reader-page-gap` spaces stacked pages when gaps are on
   (0 by default = flush, the Slice-2 look). */
.strip__chapter {
  display: flex;
  flex-direction: column;
  gap: var(--reader-page-gap, 0);
}

.strip__sentinel {
  height: 1px;
}
</style>
