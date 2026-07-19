<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
import FractionalSeriesCard from '../fractionals/FractionalSeriesCard.vue'
import type { SeriesFractionals } from './fractionals.types'

/**
 * Fractionals — the library-wide "fix fractional chapters in one place" screen.
 * Renders every series that has downloaded fractional chapters as a grid of
 * FractionalSeriesCards, each doing its whole fix inline: jump to detail, toggle
 * the whole-series ignore policy, and open the cleanup dialog.
 *
 * There is NO bulk "clean all" — a blind library-wide fractional delete would
 * violate the per-chapter, page-count-judged safety rule (the full-size-chapter
 * lesson). Cleaning stays per-series through the reused cleanup dialog.
 *
 * Presentation only: every series arrives via props and all actions are emitted —
 * no fetching, routing, or stores. An empty `series` array is the all-clear
 * EmptyState. `loading` shows skeletons; `refreshing` puts the rescan button in
 * flight; `busyIds` dims the cards whose ignore toggle is mid-write. Token-only
 * colours → both themes render.
 */
const props = withDefaults(defineProps<{
  /** The series with downloaded fractionals; empty → all-clear state. */
  series: SeriesFractionals[]
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
  /** When true, the rescan action is in flight (spinner + disabled). */
  refreshing?: boolean
  /** Series ids whose whole-series ignore toggle is mid-write (dims that card's toggle). */
  busyIds?: string[]
}>(), {
  loading: false,
  refreshing: false,
  busyIds: () => [],
})

const emit = defineEmits<{
  /** A card header was clicked — open that series' detail view. */
  'open-series': [seriesId: string]
  /** A card's ignore toggle flipped — set that series' whole-series policy. */
  'toggle-ignore': [payload: { seriesId: string, ignore: boolean }]
  /** A card's "Clean files" was clicked — open the cleanup dialog for that series. */
  'clean-files': [seriesId: string]
  /** Rescan clicked — the parent refetches GET /api/library/fractionals. */
  'refresh': []
}>()

// Nothing to clean (and not loading) → the all-clear empty state.
const isEmpty = computed(() => !props.loading && props.series.length === 0)

const skeletons = Array.from({ length: 3 }, (_, i) => i)
</script>

<template>
  <div class="fractionals">
    <!-- Intro + rescan action -->
    <div class="fractionals__head">
      <p class="fractionals__intro">
        Series with downloaded fractional chapters (5.1, 5.5 …). Set the “Ignore fractional chapters”
        policy for a whole series, then clean the leftover files — judged per chapter by page count, never in bulk.
      </p>
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
      class="fractionals__grid"
      min-tile="320px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <Skeleton v-for="n in skeletons" :key="n" variant="card" height="12.5rem" />
    </ResponsiveGrid>

    <!-- All-clear empty state -->
    <EmptyState
      v-else-if="isEmpty"
      title="No fractionals to manage"
      sub="No series has downloaded fractional chapters. Nothing to clean up here."
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
      class="fractionals__grid"
      min-tile="320px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <FractionalSeriesCard
        v-for="s in series"
        :key="s.seriesId"
        :series="s"
        :busy="busyIds.includes(s.seriesId)"
        @open-series="emit('open-series', $event)"
        @toggle-ignore="emit('toggle-ignore', $event)"
        @clean-files="emit('clean-files', $event)"
      />
    </ResponsiveGrid>
  </div>
</template>

<style scoped>
/* GROW layout, mirroring LibraryHealth: the document scrolls and the grid grows
 * with content — no viewport-keyed height, no inner scroll region. */
.fractionals {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

.fractionals__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-lg);
  flex-wrap: wrap;
  margin-bottom: var(--space-2xl-tight);
}

.fractionals__intro {
  max-width: 40rem;
  min-width: 0;
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
  overflow-wrap: anywhere;
}

.fractionals__grid {
  align-items: start;
}

/* Compact mobile density (mirrors LibraryHealth's ≤900px block). */
@media (max-width: 900px) {
  .fractionals {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
