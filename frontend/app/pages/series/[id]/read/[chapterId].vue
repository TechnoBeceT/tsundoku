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
 *     `onSeek`) updates the chrome's page slider; `visible-pages` feeds a
 *     PER-CHAPTER map (`visiblePagesByChapter`) — the slider's TRIMMED
 *     denominator is always the CENTRED chapter's own entry, never a stale
 *     count left behind by a chapter jump; `chapter-finished` marks a chapter
 *     read as the reader scrolls past its end; `resumeTarget` opens the strip
 *     at the last-read page, unless the URL carries an explicit `?page=`
 *     (the series page's "Continue" FAB — see `queryPage`'s doc comment).
 *   - ReaderChrome is a hide-on-scroll overlay (back / title / page slider /
 *     settings). A tap in the vertical CENTRE of the screen toggles it
 *     (`isCenterTap`); taps near the top/bottom edges or on a chrome control do
 *     not. ReaderSettingsSheet edits the global settings, applied to the strip as
 *     CSS custom properties on `.reader` (readerStyleVars) — padding / fit / gaps.
 *   - The chrome's page slider (`@seek`) scrolls WITHIN the current chapter via
 *     the strip's exposed `seekToPage`; its prev/next buttons navigate chapters
 *     via `goToChapter` (wraps `jumpToChapter` + a `router.replace` so the URL
 *     tracks the flipped-to chapter without growing history — see its own doc
 *     comment) — `@next` marks the chapter read FIRST (deliberately leaving a
 *     chapter forward always means "done with it"), `@prev` marks nothing
 *     (going back is a correction, not a completion).
 *
 * §16: the initial load shows a visible loading state, a hard failure shows the
 * ErrorBanner, and an empty (no downloaded chapters) series shows an EmptyState —
 * never a blank fullscreen. Progress writes are the sanctioned best-effort
 * exception (see useReadingProgress).
 */
// key: PINNED to the series id only (never the chapterId). `app.vue` renders a
// bare `<NuxtPage />` with no `:page-key`, so Nuxt's DEFAULT page key is the
// param-interpolated PATH (`generateRouteKey`/`interpolatePath` in
// nuxt/dist/pages/runtime/utils.js) — which includes `chapterId` and therefore
// CHANGES on every chapter flip, tearing this whole component down and
// remounting it on every prev/next tap. A remount reconstructs `useReader`
// (fresh `GET /api/series/{id}`, a `chapters.length === 0` flash of the
// full-screen loading state) AND `useReadingProgress` — destroying its
// in-memory `readThisSession` set, which is what stops a backward scroll from
// un-reading a chapter just finished (see `record`'s doc comment). Pinning the
// key to `/series/:id/read` makes a same-series chapter flip a genuine
// param-only route change: Vue reuses this component instance instead of
// tearing it down, so the doc comment on `goToChapter` below (which already
// assumed no remount) is now actually true. A DIFFERENT series (a different
// `id`) still produces a different key, so navigating between series' readers
// remounts correctly, as it must.
definePageMeta({ layout: 'bare', key: (route) => `/series/${String(route.params.id)}/read` })

const route = useRoute()
const router = useRouter()
const id = route.params.id as string
const chapterId = route.params.chapterId as string

const {
  chapters, mountedChapters, pageUrl, onNearTail, onNearHead, hasPrev, hasNext, setCurrentChapter,
  currentChapterId, prevChapter, nextChapter, jumpToChapter, loading, error, seriesTitle,
  scrollRequest, requestScroll,
} = useReader(id, chapterId)
const { record, markRead, resumeTarget, flush } = useReadingProgress(chapters, chapterId)
const { settings, update } = useReaderSettings()

// An explicit `?page=` overrides the recomputed resume page — carried by the
// series page's "Continue" FAB (see its `onResume` doc comment). It must win
// over recomputing via `resumeTarget` here: this route's own `startChapterId`
// is the deep-linked chapter, so `resumeTarget(chapters.value)` always hits
// its "started" branch (open at THAT chapter's own saved `lastReadPage`) —
// which, for the FAB's "every chapter read" case, is the chapter's FINAL
// page, not the page 0 `resumeTarget` actually decided at the FAB. A direct
// chapter-row click carries no `?page=`, so it still resolves via the
// "started" branch exactly as before (open at that chapter's own progress).
const queryPage = computed<number | null>(() => {
  const raw = route.query?.page
  const value = Array.isArray(raw) ? raw[0] : raw
  const n = value == null ? Number.NaN : Number(value)
  return Number.isFinite(n) && n >= 0 ? Math.trunc(n) : null
})

// Resume anchor: recomputed from the loaded chapters, with the query-param
// page override (see above) applied on top when present.
const resume = computed(() => {
  const target = resumeTarget(chapters.value)
  return queryPage.value != null ? { chapterId: target.chapterId, page: queryPage.value } : target
})

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
// chapter) and `visiblePagesByChapter` (a PER-CHAPTER map of the TRIMMED page
// count — the slider's denominator, never the declared `pageCount`; see
// ReaderPageSlider's doc comment).
//
// KEYED BY CHAPTER ID (not a single shared ref): the strip's `visible-pages`
// emit only fires after a real scroll settles on the CURRENTLY-mounted
// chapter — never at mount, never on a `jumpToChapter`. A single shared ref
// would keep the PREVIOUS chapter's count after a jump, so a slider-next
// tapped again before the new chapter is ever scrolled would mark the new
// chapter read with a count that has no relation to its real length (the
// reproduced bug: read ch-A (9 pages measured) -> next -> land on ch-B ->
// next again before scrolling B -> markRead('ch-b', 9), a number belonging to
// ch-A). Keying by chapterId means an unmeasured chapter simply has no entry
// (see `visiblePagesFor`'s fallback) instead of borrowing someone else's.
//
// `currentPage` is ALSO set optimistically by `onSeek` — see the
// feedback-loop guard below.
const currentPage = ref(0)
const visiblePagesByChapter = ref<Record<string, number>>({})

/** The TRIMMED visible-page count for one chapter — 0 (safe slider
 *  denominator; see ReaderPageSlider.logic) when that chapter hasn't been
 *  scrolled/measured yet. NEVER falls back to another chapter's count. */
function visiblePagesFor(id: string | null): number {
  if (!id) return 0
  return visiblePagesByChapter.value[id] ?? 0
}

/** The slider's live denominator: the CENTRED chapter's own measured count. */
const visiblePages = computed(() => visiblePagesFor(currentChapterId.value))

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

/** Records a chapter's TRIMMED page count, keyed by the chapter it belongs to
 *  (never overwrites a different chapter's entry — see the map's doc comment
 *  above). */
function onVisiblePages(payload: { chapterId: string, count: number }): void {
  visiblePagesByChapter.value[payload.chapterId] = payload.count
}

/** The best available page count to mark a chapter read at: its measured/
 *  trimmed count (`visiblePagesByChapter`) if the strip has actually scrolled
 *  through it this session, else its declared `pageCount` (0 if unknown). A
 *  Kaizoku import can DECLARE more pages than the CBZ really has, so the
 *  measured count — when we have it — is always the more accurate resume
 *  anchor; the declared count is only a fallback for a chapter that finished
 *  without ever emitting `visible-pages` (e.g. it fit on-screen with no
 *  scroll). Shared by `onChapterFinished` and `onSliderNext` so both mark-read
 *  paths apply the exact same rule (§2 DRY — they used to disagree). */
function measuredOrDeclaredPageCount(chapterId: string): number {
  const measured = visiblePagesByChapter.value[chapterId]
  if (measured != null) return measured
  return chapters.value.find((c) => c.id === chapterId)?.pageCount ?? 0
}

/** Mark a chapter read once its end-divider scrolls past — at its last
 *  measured (or, if unmeasured, declared) page. */
function onChapterFinished(finishedId: string): void {
  markRead(finishedId, measuredOrDeclaredPageCount(finishedId))
}

/** The chrome's page slider was clicked/dragged to a new page — scroll to it
 *  within the centred chapter and see the feedback-loop guard comment above. */
function onSeek(page: number): void {
  currentPage.value = page
  suppressCenteredPageUntil = Date.now() + SEEK_ECHO_SUPPRESS_MS
  stripRef.value?.seekToPage(page)
}

/**
 * goToChapter — prev/next chapter navigation: reseeds the reader window
 * (`jumpToChapter`) AND syncs the URL via `router.replace` (never `push` — a
 * chapter flip must not grow browser history, so the back button exits the
 * reader instead of walking back through every chapter flipped past).
 * `replace` on a param-only change of the SAME matched route record updates
 * `route.params` in place without remounting this component or re-running
 * this script — `chapterId`/`id` above are captured once as plain consts, and
 * nothing here watches `route.params`, so it cannot re-fire the resume/scroll
 * watch (already spent via `resumedOnce`) or re-construct `useReader`/
 * `useReadingProgress`.
 */
function goToChapter(targetId: string): void {
  jumpToChapter(targetId)
  void router.replace(`/series/${id}/read/${targetId}`)
}

/** The slider's prev-chapter button — going back is a correction, marks nothing. */
function onSliderPrev(): void {
  if (!prevChapter.value) return
  goToChapter(prevChapter.value.id)
}

/** The slider's next-chapter button — deliberately leaving a chapter forward
 *  always means "finished with it", so mark it read BEFORE navigating away
 *  (never leave it dangling "unread" just because the owner tapped next).
 *  Marks with the LEAVING chapter's own measured-or-declared count — resolved
 *  fresh at the moment of the tap, never a value that could belong to another
 *  chapter (a jump followed by an immediate second tap, before the new
 *  chapter has scrolled/measured, must never mark it with the count left
 *  behind by the chapter just departed). */
function onSliderNext(): void {
  if (!nextChapter.value || !currentChapterId.value) return
  const leavingId = currentChapterId.value
  markRead(leavingId, measuredOrDeclaredPageCount(leavingId))
  goToChapter(nextChapter.value.id)
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
