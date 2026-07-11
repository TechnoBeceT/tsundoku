<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useReader } from '~/composables/useReader'
import { useReadingProgress } from '~/composables/useReadingProgress'
import { useReaderSettings, readerStyleVars } from '~/composables/useReaderSettings'
import { useFullscreen } from '~/composables/useFullscreen'
import { isCenterTap } from '~/components/reader/readerChrome.logic'
import type ReaderStripComponent from '~/components/reader/ReaderStrip.vue'

/**
 * Reader route — /series/:id/read/:chapterId.
 *
 * A fullscreen long-strip reader (bare layout, no app nav chrome). Delegates all
 * data + windowing to useReader(id, chapterId), progress persistence to
 * useReadingProgress, and global display settings to useReaderSettings. Renders
 * the ReaderStrip fed by both, plus the chrome overlay + settings sheet:
 *   - The strip's `near-tail`/`near-head` drive the window append/prepend;
 *     `centered` records the live position (debounced) and (guarded — see
 *     `onSeek`) updates the chrome's page slider; `visible-pages` feeds the
 *     slider's TRIMMED denominator; `chapter-finished` marks a chapter read as
 *     the reader scrolls past its end; `resumeTarget` opens the strip at the
 *     last-read page.
 *   - ReaderChrome is a hide-on-scroll overlay (back / title / page slider /
 *     settings). A tap in the vertical CENTRE of the screen toggles it
 *     (`isCenterTap`); taps near the top/bottom edges or on a chrome control do
 *     not. ReaderSettingsSheet edits the global settings, applied to the strip as
 *     CSS custom properties on `.reader` (readerStyleVars) — padding / fit / gaps.
 *   - The chrome's page slider (`@seek`) scrolls WITHIN the current chapter via
 *     the strip's exposed `seekToPage`; its prev/next buttons navigate chapters
 *     via `jumpToChapter` — `@next` marks the chapter read FIRST (deliberately
 *     leaving a chapter forward always means "done with it"), `@prev` marks
 *     nothing (going back is a correction, not a completion).
 *
 * §16: the initial load shows a visible loading state, a hard failure shows the
 * ErrorBanner, and an empty (no downloaded chapters) series shows an EmptyState —
 * never a blank fullscreen. Progress writes are the sanctioned best-effort
 * exception (see useReadingProgress).
 */
definePageMeta({ layout: 'bare' })

const route = useRoute()
const id = route.params.id as string
const chapterId = route.params.chapterId as string

const {
  chapters, mountedChapters, pageUrl, onNearTail, onNearHead, hasPrev, hasNext, setCurrentChapter,
  currentChapterId, prevChapter, nextChapter, jumpToChapter, loading, error, seriesTitle,
  scrollRequest, requestScroll,
} = useReader(id, chapterId)
const { record, markRead, resumeTarget, flush } = useReadingProgress(chapters, chapterId)
const { settings, update } = useReaderSettings()

// Resume anchor: recomputed from the loaded chapters.
const resume = computed(() => resumeTarget(chapters.value))

// Fire the resume scroll exactly once, the first time the loaded chapters
// resolve a real target chapter (empty string before `chapters` loads). Fix 4:
// this goes through `useReader.requestScroll` — the ONE token space — rather
// than the route constructing its own `scrollRequest` literal, which used to
// hardcode `token: 1` and collide with the first `jumpToChapter` call.
let resumedOnce = false
watch(resume, (r) => {
  if (resumedOnce || !r.chapterId) return
  resumedOnce = true
  requestScroll(r.chapterId, r.page)
}, { immediate: true })

// The CSS custom properties the strip reads for padding / fit / gaps. Inherited
// by the whole `.reader` subtree, so it also covers ReaderPage's reserve var.
const readerStyle = computed(() => readerStyleVars(settings))

// ---- chrome + settings state ------------------------------------------------
const chromeVisible = ref(true)
const settingsOpen = ref(false)

// Fullscreen: take the reader container edge-to-edge in-browser (a bonus over the
// installed PWA's `standalone` display). `readerEl` is the element we fullscreen.
const readerEl = ref<HTMLElement | null>(null)
const { supported: fullscreenSupported, isFullscreen, toggle: toggleFullscreen } = useFullscreen()

/** Enter/exit fullscreen on the reader container (from the chrome's button). */
function onToggleFullscreen(): void {
  if (readerEl.value) void toggleFullscreen(readerEl.value)
}

// Template ref to the strip — `seekToPage` (its `defineExpose`) is how the
// chrome's page slider scrolls within the centred chapter (see `onSeek`).
const stripRef = ref<InstanceType<typeof ReaderStripComponent> | null>(null)

// The chrome's page-slider state: `currentPage` (0-based, within the CENTRED
// chapter) and `visiblePages` (that chapter's TRIMMED page count — the
// slider's denominator, never the declared `pageCount`; see
// ReaderPageSlider's doc comment). Both are updated from the strip's
// `centered`/`visible-pages` emits; `currentPage` is ALSO set optimistically
// by `onSeek` — see the feedback-loop guard below.
const currentPage = ref(0)
const visiblePages = ref(0)

/** The chapter the reader is currently centred on (null before the first `centered` emit). */
const centeredChapter = computed(() =>
  chapters.value.find((c) => c.id === currentChapterId.value) ?? null)

/** The chrome's chapter label, e.g. "Chapter 12 · Title" (number-less → the name). */
const chapterLabel = computed(() => {
  const c = centeredChapter.value
  if (!c) return ''
  const numbered = c.number != null ? `Chapter ${c.number}` : ''
  return [numbered, c.name].filter(Boolean).join(' · ')
})

// ---- slider <-> strip feedback-loop guard ------------------------------------
// A seek scrolls the strip programmatically (`seekToPage`), and the strip's own
// throttled scroll handler then reports that scroll straight back as a
// `centered` event — it has no way to tell "I moved because of a seek" apart
// from "I moved because the owner scrolled". Left unguarded, that echo would
// overwrite `currentPage` with whatever page the VIEWPORT MIDPOINT lands on
// after the seek, which is NOT necessarily the exact page sought
// (`seekToPage` aligns the target page's TOP to the viewport top; `centered`
// reports the page under the viewport MIDPOINT — a different anchor for any
// page taller than half the viewport) — so the thumb would visibly snap away
// from the pointer mid-drag, "fighting" it.
//
// GUARD (chosen): `onSeek` sets `currentPage` OPTIMISTICALLY, so the thumb
// tracks the pointer immediately with no round-trip lag, and opens a short
// suppression window comfortably longer than the strip's own 120ms scroll
// throttle. Any `centered` event landing inside that window is still used for
// progress recording + chapter tracking (both idempotent/harmless — the
// chapter never changes from a seek, and re-recording the same-ish position is
// a no-op against the debounce's own dedup) but does NOT overwrite
// `currentPage`. Only a `centered` that fires AFTER the window closes — i.e.
// genuine scrolling, not the seek's own echo — is allowed to move the thumb.
const SEEK_ECHO_SUPPRESS_MS = 250
let suppressCenteredPageUntil = 0

/** Persist the live reading position and track the centred chapter in
 *  `useReader` — this is what `hasPrev`/`hasNext` (and the head sentinel's
 *  prepend gate) resolve against. Guarded against a seek's own scroll echo. */
function onCentered(payload: { chapterId: string, page: number }): void {
  record(payload.chapterId, payload.page)
  setCurrentChapter(payload.chapterId)
  if (Date.now() < suppressCenteredPageUntil) return
  currentPage.value = payload.page
}

/** Tracks the centred chapter's TRIMMED page count — the slider's live denominator. */
function onVisiblePages(payload: { chapterId: string, count: number }): void {
  visiblePages.value = payload.count
}

/** Mark a chapter read once its end-divider scrolls past — at its last page. */
function onChapterFinished(finishedId: string): void {
  const chapter = chapters.value.find((c) => c.id === finishedId)
  markRead(finishedId, chapter?.pageCount ?? 0)
}

/** The chrome's page slider was clicked/dragged to a new page — scroll to it
 *  within the centred chapter and see the feedback-loop guard comment above. */
function onSeek(page: number): void {
  currentPage.value = page
  suppressCenteredPageUntil = Date.now() + SEEK_ECHO_SUPPRESS_MS
  stripRef.value?.seekToPage(page)
}

/** The slider's prev-chapter button — going back is a correction, marks nothing. */
function onSliderPrev(): void {
  if (!prevChapter.value) return
  jumpToChapter(prevChapter.value.id)
}

/** The slider's next-chapter button — deliberately leaving a chapter forward
 *  always means "finished with it", so mark it read BEFORE navigating away
 *  (never leave it dangling "unread" just because the owner tapped next). */
function onSliderNext(): void {
  if (!nextChapter.value || !currentChapterId.value) return
  markRead(currentChapterId.value, visiblePages.value)
  jumpToChapter(nextChapter.value.id)
}

function backToSeries(): void {
  void navigateTo(`/series/${id}`)
}

/**
 * onReaderTap — a click anywhere in the reader toggles the chrome, UNLESS it
 * landed on a chrome control (guarded by the `data-reader-chrome` ancestor) or
 * near the top/bottom edge (where the bars live). Keeps the toggle to deliberate
 * centre taps so reading gestures near the chrome don't flip it.
 */
function onReaderTap(event: MouseEvent): void {
  if ((event.target as HTMLElement).closest('[data-reader-chrome]')) return
  if (isCenterTap(event.clientY, window.innerHeight)) chromeVisible.value = !chromeVisible.value
}

// Flush the pending debounced write on leave so the last position is never lost.
onBeforeUnmount(flush)
onBeforeRouteLeave(() => { flush() })
</script>

<template>
  <div ref="readerEl" class="reader" :style="readerStyle" @click="onReaderTap">
    <div v-if="loading && chapters.length === 0" class="reader__center reader__status">
      Loading chapter…
    </div>
    <div v-else-if="error" class="reader__center">
      <ErrorBanner :message="error" :dismissible="false" />
    </div>
    <div v-else-if="chapters.length === 0" class="reader__center">
      <EmptyState title="No downloaded chapters" sub="This series has no chapters on disk to read yet.">
        <AppButton variant="ghost" size="sm" @click="backToSeries">Back to series</AppButton>
      </EmptyState>
    </div>
    <ReaderStrip
      v-else
      ref="stripRef"
      :chapters="chapters"
      :mounted-chapters="mountedChapters"
      :page-url="pageUrl"
      :has-prev="hasPrev"
      :scroll-request="scrollRequest"
      @near-tail="onNearTail"
      @near-head="onNearHead"
      @centered="onCentered"
      @visible-pages="onVisiblePages"
      @chapter-finished="onChapterFinished"
    />

    <ReaderChrome
      :visible="chromeVisible"
      :title="seriesTitle"
      :chapter-label="chapterLabel"
      :page="currentPage"
      :visible-pages="visiblePages"
      :has-prev="hasPrev"
      :has-next="hasNext"
      :fullscreen-supported="fullscreenSupported"
      :fullscreen="isFullscreen"
      @back="backToSeries"
      @toggle-settings="settingsOpen = !settingsOpen"
      @toggle-fullscreen="onToggleFullscreen"
      @seek="onSeek"
      @prev="onSliderPrev"
      @next="onSliderNext"
    />
    <ReaderSettingsSheet
      v-model:open="settingsOpen"
      :settings="settings"
      @change="update"
    />
  </div>
</template>

<style scoped>
.reader {
  position: relative;
  height: 100vh;
  background: var(--bg);
}

.reader__center {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  padding: 24px;
}

.reader__status {
  color: var(--muted);
  font-size: var(--text-sm);
}
</style>
