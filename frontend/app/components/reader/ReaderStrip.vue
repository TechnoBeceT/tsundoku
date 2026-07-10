<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import ReaderPage from './ReaderPage.vue'
import ChapterDivider from './ChapterDivider.vue'
import type { ReaderChapter } from '~/composables/useReader'
import {
  shouldAppend,
  centeredPage,
  finishedChapterIds,
  trimTrailingFailures,
  scrollAfterReflow,
  type PageRect,
} from './ReaderStrip.logic'

/**
 * ReaderStrip — the vertical long-strip reader core. Renders the mounted window
 * of chapters (from `useReader`) as stacked `ReaderPage`s separated by
 * `ChapterDivider`s, and drives the infinite-scroll behaviour:
 *
 *   - A tail-sentinel IntersectionObserver appends the next chapter early
 *     (emits `near-tail`; the page wires it to `useReader.onNearTail`, which
 *     appends below AND unmounts far-above chapters to bound the DOM).
 *   - On each window reflow it PRESERVES the read position by anchoring on the
 *     retained tail chapter (`scrollAfterReflow`), so the seam never jumps when
 *     a far-above chapter unmounts.
 *   - A throttled scroll handler emits `centered` (the page under the viewport
 *     midpoint) and `chapter-finished` (when a chapter's end-divider scrolls
 *     above the viewport top). Slice 3 consumes both to persist/resume progress;
 *     this slice only emits them.
 *   - Applies the pageCount tail-404 tolerance: a page that fails to load and
 *     forms the contiguous end of a chapter is trimmed (declared count may
 *     exceed the real CBZ) so the reader advances; a mid-chapter failure keeps
 *     its placeholder (`trimTrailingFailures`).
 *
 * The pure decisions live in ReaderStrip.logic.ts (unit-tested); this SFC only
 * wires them to the DOM. Reader chrome + settings (padding/fit/gaps) are Slice 4
 * — sane defaults are hardcoded here (see the `<style>` note).
 */
const props = defineProps<{
  /** The full downloaded chapter list (for next-chapter labels + hasNext). */
  chapters: ReaderChapter[]
  /** The chapters currently mounted (the window `useReader` maintains). */
  mountedChapters: ReaderChapter[]
  /** Builds the same-origin page-bytes URL for (chapterId, 0-based page). */
  pageUrl: (chapterId: string, n: number) => string
}>()

const emit = defineEmits<{
  /** The tail sentinel appeared — append the next chapter. */
  'near-tail': []
  /** The page under the viewport midpoint changed (throttled). */
  'centered': [payload: { chapterId: string, page: number }]
  /** A chapter's end-divider scrolled above the viewport top. */
  'chapter-finished': [chapterId: string]
}>()

const scrollEl = ref<HTMLElement | null>(null)
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

// ---- window reflow: preserve the read position (anchor on the tail chapter) ---
// Used for BOTH the append (unmount-above) path AND the pageCount tail-404 trim
// path: any DOM change that can remove height ABOVE the viewport is bracketed by
// beforeReflow → afterReflow so the viewed position never jumps. `reflowPending`
// coalesces overlapping brackets in one tick (e.g. several trailing pages 404 at
// once) to a single snapshot→restore, so the earliest pre-change snapshot wins.
let anchorId: string | null = null
let anchorPrevTop = 0
let prevScrollTop = 0
let reflowPending = false

/** Content-relative top of an element inside the scroll container (getBoundingClientRect based, offsetParent-agnostic). */
function contentTop(el: HTMLElement, container: HTMLElement): number {
  return el.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop
}

/** Snapshot the retained tail chapter's position just before a reflow (once per tick). */
function beforeReflow(): void {
  if (reflowPending) return
  const el = scrollEl.value
  if (!el) return
  anchorId = props.mountedChapters.at(-1)?.id ?? null
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

// ---- tail sentinel: append the next chapter ---------------------------------
let observer: IntersectionObserver | null = null

function onSentinel(entries: IntersectionObserverEntry[]): void {
  const entry = entries[0]
  if (!entry?.isIntersecting) return
  if (!shouldAppend(true, hasNext.value)) return
  beforeReflow()
  emit('near-tail')
  void afterReflow()
}

// ---- scroll: emit centered + chapter-finished (throttled) -------------------
const THROTTLE_MS = 120
let lastRun = 0
let pendingTimer: ReturnType<typeof setTimeout> | null = null
const emittedFinished = new Set<string>()

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
  if (centered) emit('centered', centered)

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
  if (el && sentinelEl.value) {
    // rootMargin prefetches the next chapter ~a viewport early so the seam is seamless.
    observer = new IntersectionObserver(onSentinel, { root: el, rootMargin: '600px 0px' })
    observer.observe(sentinelEl.value)
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
      <template v-for="chapter in mountedChapters" :key="chapter.id">
        <div class="strip__chapter" :data-chapter-id="chapter.id">
          <ReaderPage
            v-for="n in visiblePages(chapter)"
            :key="n"
            :data-chapter-id="chapter.id"
            :data-page="n - 1"
            :src="pageUrl(chapter.id, n - 1)"
            :alt="`Page ${n}`"
            @error="onPageError(chapter.id, n - 1)"
          />
        </div>
        <ChapterDivider
          :data-divider-id="chapter.id"
          :finished="finishedRef(chapter)"
          :next="nextRef(chapter)"
        />
      </template>
      <div ref="sentinelEl" class="strip__sentinel" aria-hidden="true" />
    </div>
  </div>
</template>

<style scoped>
/* Slice 4 note: the reader background, column width, and page gap are hardcoded
   sane defaults here; the reader-settings slice will drive them from CSS vars. */
.strip {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--bg);
  -webkit-overflow-scrolling: touch;
  overscroll-behavior: contain;
}

.strip__col {
  max-width: 800px;
  margin: 0 auto;
}

.strip__chapter {
  display: block;
}

.strip__sentinel {
  height: 1px;
}
</style>
