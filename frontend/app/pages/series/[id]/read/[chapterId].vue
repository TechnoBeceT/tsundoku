<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useReader } from '~/composables/useReader'
import { useReadingProgress } from '~/composables/useReadingProgress'

/**
 * Reader route — /series/:id/read/:chapterId.
 *
 * A fullscreen long-strip reader (bare layout, no app nav chrome). Delegates all
 * data + windowing to useReader(id, chapterId), progress persistence to
 * useReadingProgress, and renders the ReaderStrip fed by both: the strip's
 * `near-tail` drives the window append, `centered` records the live position
 * (debounced), `chapter-finished` marks a chapter read, and the computed
 * `resumeTarget` opens the strip at the last-read page.
 *
 * §16: the initial load shows a visible loading state, a hard failure shows the
 * ErrorBanner, and an empty (no downloaded chapters) series shows an EmptyState —
 * never a blank fullscreen. Progress writes are the sanctioned best-effort
 * exception (see useReadingProgress). Reader chrome + settings are Slice 4; a
 * minimal "back to series" affordance keeps the owner from being trapped.
 */
definePageMeta({ layout: 'bare' })

const route = useRoute()
const id = route.params.id as string
const chapterId = route.params.chapterId as string

const { chapters, mountedChapters, pageUrl, onNearTail, loading, error } = useReader(id, chapterId)
const { record, markRead, resumeTarget, flush } = useReadingProgress(chapters, chapterId)

// Resume anchor: recomputed from the loaded chapters; ReaderStrip applies it once
// on mount (it only mounts after chapters load, so the target is ready by then).
const resume = computed(() => resumeTarget(chapters.value))

/** Persist the live reading position as the owner scrolls (debounced + deduped). */
function onCentered(payload: { chapterId: string, page: number }): void {
  record(payload.chapterId, payload.page)
}

/** Mark a chapter read once its end-divider scrolls past — at its last page. */
function onChapterFinished(finishedId: string): void {
  const chapter = chapters.value.find((c) => c.id === finishedId)
  markRead(finishedId, chapter?.pageCount ?? 0)
}

function backToSeries(): void {
  void navigateTo(`/series/${id}`)
}

// Flush the pending debounced write on leave so the last position is never lost.
onBeforeUnmount(flush)
onBeforeRouteLeave(() => { flush() })
</script>

<template>
  <div class="reader">
    <!-- Minimal escape hatch until Slice 4's reader chrome lands. -->
    <button class="reader__back" type="button" aria-label="Back to series" @click="backToSeries">
      <Icon name="lucide:arrow-left" size="18" />
    </button>

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
      :initial-scroll-to="resume"
      @near-tail="onNearTail"
      @centered="onCentered"
      @chapter-finished="onChapterFinished"
    />
  </div>
</template>

<style scoped>
.reader {
  position: relative;
  height: 100vh;
  background: var(--bg);
}

.reader__back {
  position: fixed;
  top: 14px;
  left: 14px;
  z-index: 10;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border: 1px solid var(--border2);
  border-radius: var(--radius-pill);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
}

.reader__back:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}

.reader__back:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
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
