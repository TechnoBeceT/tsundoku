<script setup lang="ts">
import { computed, ref } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
import SourcelessSeriesCard from '../sourceless/SourcelessSeriesCard.vue'
import SourcelessCleanupDialog from '../seriesDetail/SourcelessCleanupDialog.vue'
import { useSourceless } from '../../composables/useSourceless'
import type { SourcelessCleanupPreview } from './sourceless.types'

/**
 * Sourceless — the library-wide "clean up sourceless chapters in one place"
 * screen (`GET /api/library/sourceless`). Renders every series with downloaded
 * chapters no remaining source carries — the CBZs `RemoveProvider` deliberately
 * kept on disk (never-auto-delete, Rule 2; GAP-101/QCAT-303) — as a grid of
 * SourcelessSeriesCards, each with one "Review" action.
 *
 * Unlike `Fractionals` (presentation-only, driven by props from its page) this
 * screen owns `useSourceless()` and the reused `SourcelessCleanupDialog`
 * directly: there is no whole-series policy to toggle, so the ONLY page-level
 * concern would be routing, and there isn't any here — reviewing a series opens
 * the dialog on THIS screen, never a per-page state machine. Clicking "Review"
 * fetches that series' removable preview and opens the dialog; a successful
 * removal (§16: failures stay inside the dialog, never silently closed) re-polls
 * the list and closes it.
 *
 * There is NO bulk "clean all" — same per-series safety posture as Fractionals.
 */
const {
  series,
  pending,
  refreshing,
  error,
  removeBusy,
  removeError,
  refresh,
  fetchPreview,
  removeSourceless,
} = useSourceless()

// Nothing to review (and not loading) → the all-clear empty state.
const isEmpty = computed(() => !pending.value && series.value.length === 0)

const skeletons = Array.from({ length: 3 }, (_, i) => i)

// ---- Per-series cleanup dialog ----------------------------------------------
const dialogOpen = ref(false)
const activeSeriesId = ref<string | null>(null)
const previewLoading = ref(false)
const preview = ref<SourcelessCleanupPreview | null>(null)

const activeSeriesTitle = computed(() =>
  series.value.find((s) => s.seriesId === activeSeriesId.value)?.displayName ?? '',
)

// The one card whose review/removal flow is in flight (only one dialog open at
// a time), so only THAT card's button spins.
const busySeriesId = computed(() => (
  (previewLoading.value || removeBusy.value) ? activeSeriesId.value : null
))

async function onReview(seriesId: string): Promise<void> {
  activeSeriesId.value = seriesId
  previewLoading.value = true
  preview.value = await fetchPreview(seriesId)
  previewLoading.value = false
  dialogOpen.value = true
}

async function onConfirm(chapterIds: string[]): Promise<void> {
  if (!activeSeriesId.value) return
  const ok = await removeSourceless(activeSeriesId.value, chapterIds)
  if (ok) {
    dialogOpen.value = false
    activeSeriesId.value = null
    preview.value = null
  }
}

function onCloseDialog(): void {
  dialogOpen.value = false
}
</script>

<template>
  <div class="sourceless">
    <ErrorBanner v-if="error" :message="error" />

    <!-- Intro + rescan action -->
    <div class="sourceless__head">
      <div class="sourceless__intro">
        <h1 class="sourceless__title">Sourceless chapters</h1>
        <p class="sourceless__sub">
          Downloaded chapters no source carries — e.g. left behind when a source was removed.
        </p>
      </div>
      <AppButton variant="mini" :loading="refreshing" @click="refresh">
        <template #icon>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-2.6-6.4" />
            <path d="M21 3v6h-6" />
          </svg>
        </template>
        {{ refreshing ? 'Rescanning…' : 'Rescan' }}
      </AppButton>
    </div>

    <!-- Loading skeletons -->
    <ResponsiveGrid
      v-if="pending"
      class="sourceless__grid"
      min-tile="320px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <Skeleton v-for="n in skeletons" :key="n" variant="card" height="9.5rem" />
    </ResponsiveGrid>

    <!-- All-clear empty state -->
    <EmptyState
      v-else-if="isEmpty"
      title="Nothing sourceless"
      sub="Every downloaded chapter has a source. Nothing to clean up here."
      icon-tone="sd-hl-ok-dot"
    >
      <template #icon>
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 6L9 17l-5-5" />
        </svg>
      </template>
    </EmptyState>

    <!-- Series cards -->
    <ResponsiveGrid
      v-else
      class="sourceless__grid"
      min-tile="320px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <SourcelessSeriesCard
        v-for="s in series"
        :key="s.seriesId"
        :row="s"
        :busy="busySeriesId === s.seriesId"
        @review="onReview"
      />
    </ResponsiveGrid>

    <SourcelessCleanupDialog
      :open="dialogOpen"
      :series-title="activeSeriesTitle"
      :preview="preview"
      :busy="removeBusy"
      :error="removeError"
      @close="onCloseDialog"
      @confirm="onConfirm"
    />
  </div>
</template>

<style scoped>
/* GROW layout, mirroring Fractionals/LibraryHealth: the document scrolls and
 * the grid grows with content — no viewport-keyed height, no inner scroll region. */
.sourceless {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

.sourceless__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-lg);
  flex-wrap: wrap;
  margin-bottom: var(--space-2xl-tight);
}

.sourceless__intro {
  max-width: 40rem;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
}

.sourceless__title {
  margin: 0;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
}

.sourceless__sub {
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
  overflow-wrap: anywhere;
}

.sourceless__grid {
  align-items: start;
}

/* Compact mobile density (mirrors Fractionals' ≤900px block). */
@media (max-width: 900px) {
  .sourceless {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
