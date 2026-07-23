<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
import SourcelessSeriesCard from '../sourceless/SourcelessSeriesCard.vue'
import type { SeriesSourceless } from './sourceless.types'

/**
 * Sourceless — the library-wide "clean up sourceless chapters in one place"
 * screen (`GET /api/library/sourceless`). Renders every series with downloaded
 * chapters no remaining source carries — the CBZs `RemoveProvider` deliberately
 * kept on disk (never-auto-delete, Rule 2; GAP-101/QCAT-303) — as a grid of
 * SourcelessSeriesCards, each with one "Review" action.
 *
 * Presentation only, mirroring Fractionals exactly: every series arrives via
 * props and all actions are emitted — no fetching, routing, or the reused
 * cleanup dialog. An empty `series` array is the all-clear EmptyState.
 * `loading` shows skeletons; `refreshing` puts the rescan button in flight.
 * Unlike Fractionals there is no whole-series ignore-policy toggle, so there is
 * no `busyIds` prop either — the parent page owns `useSourceless()` and the
 * per-series `SourcelessCleanupDialog` directly (§16: it closes the dialog only
 * on removal success, and shows a failure inside it otherwise). Token-only
 * colours → both themes render.
 *
 * There is NO bulk "clean all" — same per-series safety posture as Fractionals.
 */
const props = withDefaults(defineProps<{
  /** The series with downloaded sourceless chapters; empty → all-clear state. */
  series: SeriesSourceless[]
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
  /** When true, the rescan action is in flight (spinner + disabled). */
  refreshing?: boolean
}>(), {
  loading: false,
  refreshing: false,
})

const emit = defineEmits<{
  /** A card's "Review" was clicked — the parent fetches that series' removable preview and opens the cleanup dialog. */
  'review': [seriesId: string]
  /** Rescan clicked — the parent refetches GET /api/library/sourceless. */
  'refresh': []
}>()

// Nothing to review (and not loading) → the all-clear empty state.
const isEmpty = computed(() => !props.loading && props.series.length === 0)

const skeletons = Array.from({ length: 3 }, (_, i) => i)
</script>

<template>
  <div class="sourceless">
    <!-- Intro + rescan action -->
    <div class="sourceless__head">
      <div class="sourceless__intro">
        <h1 class="sourceless__title">Sourceless chapters</h1>
        <p class="sourceless__sub">
          Downloaded chapters no source carries — e.g. left behind when a source was removed.
        </p>
      </div>
      <AppButton variant="mini" :loading="refreshing" @click="emit('refresh')">
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
      v-if="loading"
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
        :busy="false"
        @review="emit('review', $event)"
      />
    </ResponsiveGrid>
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
