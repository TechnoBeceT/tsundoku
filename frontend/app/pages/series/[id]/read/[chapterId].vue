<script setup lang="ts">
import { computed, ref, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useReader, type ScrollRequest } from '~/composables/useReader'
import { useReadingProgress } from '~/composables/useReadingProgress'
import { useReaderSettings, readerStyleVars } from '~/composables/useReaderSettings'
import { useFullscreen } from '~/composables/useFullscreen'
import { isCenterTap } from '~/components/reader/readerChrome.logic'

/**
 * Reader route — /series/:id/read/:chapterId.
 *
 * A fullscreen long-strip reader (bare layout, no app nav chrome). Delegates all
 * data + windowing to useReader(id, chapterId), progress persistence to
 * useReadingProgress, and global display settings to useReaderSettings. Renders
 * the ReaderStrip fed by both, plus the Slice-4 chrome overlay + settings sheet:
 *   - The strip's `near-tail` drives the window append; `centered` records the
 *     live position (debounced) AND feeds the chrome's page/percent readout;
 *     `chapter-finished` marks a chapter read; `resumeTarget` opens the strip at
 *     the last-read page.
 *   - ReaderChrome is a hide-on-scroll overlay (back / title / progress /
 *     settings). A tap in the vertical CENTRE of the screen toggles it
 *     (`isCenterTap`); taps near the top/bottom edges or on a chrome control do
 *     not. ReaderSettingsSheet edits the global settings, applied to the strip as
 *     CSS custom properties on `.reader` (readerStyleVars) — padding / fit / gaps.
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
  chapters, mountedChapters, pageUrl, onNearTail, onNearHead, hasPrev, setCurrentChapter, loading, error, seriesTitle,
} = useReader(id, chapterId)
const { record, markRead, resumeTarget, flush } = useReadingProgress(chapters, chapterId)
const { settings, update } = useReaderSettings()

// Resume anchor: recomputed from the loaded chapters.
const resume = computed(() => resumeTarget(chapters.value))

// The strip's initial scroll-to-target — the resume anchor, published once as a
// token-1 request the first time it resolves to a real chapter (ReaderStrip only
// mounts after chapters load, so this is ready by the time it renders). A future
// chapter-jump feature will need to merge this with `useReader.scrollRequest`
// (its own, separately-numbered token space) into one prop value.
const scrollRequest = computed<ScrollRequest | null>(() => {
  const r = resume.value
  return r.chapterId ? { chapterId: r.chapterId, page: r.page, token: 1 } : null
})

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

// The last centered position (0-based page within a chapter) — drives the
// chrome's page/percent readout. Updated on every throttled `centered` emit.
const lastCentered = ref<{ chapterId: string, page: number } | null>(null)

/** The chapter the reader is currently centred on (null before the first emit). */
const centeredChapter = computed(() =>
  chapters.value.find((c) => c.id === lastCentered.value?.chapterId) ?? null)

/** The chrome's chapter label, e.g. "Chapter 12 · Title" (number-less → the name). */
const chapterLabel = computed(() => {
  const c = centeredChapter.value
  if (!c) return ''
  const numbered = c.number != null ? `Chapter ${c.number}` : ''
  return [numbered, c.name].filter(Boolean).join(' · ')
})

/** The chrome's "page X / N" readout for the centred chapter. */
const pageLabel = computed(() => {
  const c = centeredChapter.value
  if (!c || !lastCentered.value) return ''
  return `${lastCentered.value.page + 1} / ${c.pageCount}`
})

/** Series-level progress 0–100: whole chapters read + the intra-chapter fraction. */
const percent = computed(() => {
  const c = centeredChapter.value
  const total = chapters.value.length
  if (!c || !lastCentered.value || total === 0) return 0
  const idx = chapters.value.findIndex((ch) => ch.id === c.id)
  const intra = c.pageCount > 0 ? (lastCentered.value.page + 1) / c.pageCount : 0
  return ((idx + intra) / total) * 100
})

/** Persist the live reading position, update the chrome readout, AND track the
 *  centred chapter in `useReader` — this is what `hasPrev`/`hasNext` (and the
 *  head sentinel's prepend gate) resolve against. */
function onCentered(payload: { chapterId: string, page: number }): void {
  lastCentered.value = payload
  record(payload.chapterId, payload.page)
  setCurrentChapter(payload.chapterId)
}

/** Mark a chapter read once its end-divider scrolls past — at its last page. */
function onChapterFinished(finishedId: string): void {
  const chapter = chapters.value.find((c) => c.id === finishedId)
  markRead(finishedId, chapter?.pageCount ?? 0)
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
      :chapters="chapters"
      :mounted-chapters="mountedChapters"
      :page-url="pageUrl"
      :has-prev="hasPrev"
      :scroll-request="scrollRequest"
      @near-tail="onNearTail"
      @near-head="onNearHead"
      @centered="onCentered"
      @chapter-finished="onChapterFinished"
    />

    <ReaderChrome
      :visible="chromeVisible"
      :title="seriesTitle"
      :chapter-label="chapterLabel"
      :page-label="pageLabel"
      :percent="percent"
      :fullscreen-supported="fullscreenSupported"
      :fullscreen="isFullscreen"
      @back="backToSeries"
      @toggle-settings="settingsOpen = !settingsOpen"
      @toggle-fullscreen="onToggleFullscreen"
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
