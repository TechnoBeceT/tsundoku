<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
import DuplicateSeriesCard from '../duplicates/DuplicateSeriesCard.vue'
import { formatBytes } from '~/utils/fileSize'
import type { SeriesDuplicateFiles } from './duplicates.types'

/**
 * Duplicates — the Cleanup console's third tab (`GET /api/library/duplicate-files`).
 * Renders every series whose folder holds leftover CBZs the per-series "Remove
 * duplicate files" action would delete, as a grid of DuplicateSeriesCards.
 *
 * DISCOVERY ONLY, and that is the whole design: there is no library-wide removal
 * endpoint, so no card deletes anything and there is no bulk action. Each card
 * opens its series, where the existing owner-triggered button lives. The tab
 * exists because the owner previously had to open every series in turn to find
 * out which ones needed it.
 *
 * A series whose leftover filenames cannot be parsed as a plain chapter number is
 * deliberately ABSENT — the backend's strict match refuses to touch them, so
 * listing them here would promise a cleanup that would not happen.
 *
 * Presentation only, mirroring Sourceless exactly: every series arrives via props
 * and all actions are emitted. An empty `series` array is the all-clear
 * EmptyState; `loading` shows skeletons; `refreshing` puts the rescan button in
 * flight. Token-only colours → both themes render.
 */
const props = withDefaults(defineProps<{
  /** The series with removable duplicate files; empty → all-clear state. */
  series: SeriesDuplicateFiles[]
  /**
   * Total removable files across the library. REQUIRED, and taken verbatim from
   * the server rather than summed from `series` — the backend owns the figure, so
   * re-deriving it here would be a second answer waiting to disagree (§11).
   */
  totalFiles: number
  /** Total reclaimable bytes across the library — server-supplied, same rule. */
  totalBytes: number
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
  /** When true, the rescan action is in flight (spinner + disabled). */
  refreshing?: boolean
}>(), {
  loading: false,
  refreshing: false,
})

const emit = defineEmits<{
  /** A card's "Open series" was clicked — the parent navigates to that series. */
  'open-series': [seriesId: string]
  /** Rescan clicked — the parent refetches GET /api/library/duplicate-files. */
  'refresh': []
}>()

// Nothing to clean (and not loading) → the all-clear empty state.
const isEmpty = computed(() => !props.loading && props.series.length === 0)

// The header roll-up — the server's own totals, only formatted for display.
const summary = computed(() =>
  `${props.series.length} series · ${props.totalFiles} file${props.totalFiles === 1 ? '' : 's'} · ${formatBytes(props.totalBytes)}`,
)

const skeletons = Array.from({ length: 3 }, (_, i) => i)
</script>

<template>
  <div class="duplicates">
    <!-- Intro + rescan action -->
    <div class="duplicates__head">
      <div class="duplicates__intro">
        <h1 class="duplicates__title">Duplicate files</h1>
        <p class="duplicates__sub">
          Leftover CBZs of chapters that already have a winning file. Open a series to remove them.
        </p>
        <p v-if="!loading && series.length" class="duplicates__summary">{{ summary }}</p>
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
      class="duplicates__grid"
      min-tile="320px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <Skeleton v-for="n in skeletons" :key="n" variant="card" height="9.5rem" />
    </ResponsiveGrid>

    <!-- All-clear empty state -->
    <EmptyState
      v-else-if="isEmpty"
      title="No duplicate files"
      sub="Every chapter has exactly one file on disk. Nothing to clean up here."
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
      class="duplicates__grid"
      min-tile="320px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <DuplicateSeriesCard
        v-for="s in series"
        :key="s.seriesId"
        :row="s"
        @open="emit('open-series', $event)"
      />
    </ResponsiveGrid>
  </div>
</template>

<style scoped>
/* GROW layout, mirroring Sourceless/Fractionals: the document scrolls and the
 * grid grows with content — no viewport-keyed height, no inner scroll region. */
.duplicates {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

.duplicates__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-lg);
  flex-wrap: wrap;
  margin-bottom: var(--space-2xl-tight);
}

.duplicates__intro {
  max-width: 40rem;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
}

.duplicates__title {
  margin: 0;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
}

.duplicates__sub {
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
  overflow-wrap: anywhere;
}

.duplicates__summary {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.duplicates__grid {
  align-items: start;
}

/* Compact mobile density (mirrors Sourceless' ≤900px block). */
@media (max-width: 900px) {
  .duplicates {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
