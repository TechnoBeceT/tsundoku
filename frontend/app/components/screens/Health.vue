<script setup lang="ts">
import ErrorBanner from '../ui/ErrorBanner.vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import LibraryHealth from './LibraryHealth.vue'
import SourceHealth from './SourceHealth.vue'
import { HEALTH_TABS, type HealthTab } from '~/utils/healthTabs'
import type { SeriesHealth } from './libraryHealth.types'
import type { SourceMetric } from './sourceHealth.types'

/**
 * Health — the top-level `/health` console: a 2-tab screen composing the two
 * source-health surfaces that used to live apart.
 *   - Library → the existing LibraryHealth screen (series-centric, unchanged).
 *   - Sources → the new SourceHealth screen (source-centric metrics; the
 *     Kaizoku-grade report is slice 4).
 *
 * Presentation only: the active tab is CONTROLLED (the composition root
 * `pages/health.vue` owns it + persists it to sessionStorage + reads the `?tab=`
 * deep-link), and every child action is forwarded up as an emit. This shell owns
 * only the tab bar + which tab renders; each tab's data + §16 state arrive via
 * props (the Sources data is fetched lazily by the page, only once its tab is
 * first shown). Token-only colours → both themes.
 */
withDefaults(defineProps<{
  /** Which tab is showing (controlled — the tab bar emits `set-tab`). */
  activeTab?: HealthTab
  // ── Library tab ──────────────────────────────────────────────────────────
  /** The sick series to display; empty → all-clear state. */
  series: SeriesHealth[]
  /** Whether the library-health list is loading (skeleton cards). */
  healthLoading?: boolean
  /** Whether the library-health rescan is in flight. */
  refreshing?: boolean
  /** A library-health load failure, shown as a page-level banner (§16). */
  healthError?: string | null
  // ── Sources tab ──────────────────────────────────────────────────────────
  /** The per-source metric rows (slowest-first). */
  metrics?: SourceMetric[]
  /** Whether the source-metrics list is loading. */
  sourcePending?: boolean
  /** A source-metrics load failure, surfaced inline in the pane. */
  sourceError?: string | null
  /** Whether a warm-up pass is in flight. */
  warming?: boolean
  /** The last warm-up's success note. */
  warmMessage?: string | null
  /** The last warm-up's failure message. */
  warmError?: string | null
  /** The source id whose breaker reset is in flight (null when none). */
  resetting?: string | null
  /** The last breaker-reset failure message. */
  resetError?: string | null
}>(), {
  activeTab: 'library',
  healthLoading: false,
  refreshing: false,
  healthError: null,
  metrics: () => [],
  sourcePending: false,
  sourceError: null,
  warming: false,
  warmMessage: null,
  warmError: null,
  resetting: null,
  resetError: null,
})

const emit = defineEmits<{
  /** A tab was selected — carries its key. */
  'set-tab': [tab: HealthTab]
  /** A sick-series card was clicked — open that series' detail (Library tab). */
  'open-series': [seriesId: string]
  /** Rescan library health (Library tab). */
  'refresh': []
  /** Trigger a manual warm-up pass across all sources (Sources tab). */
  'warm-now': []
  /** Reset a source's tripped circuit-breaker — carries the source id (Sources tab). */
  'reset-breaker': [id: string]
}>()
</script>

<template>
  <div class="health-console">
    <div class="health-console__tabs">
      <SegmentedTabs
        :model-value="activeTab"
        :tabs="HEALTH_TABS"
        @update:model-value="emit('set-tab', $event as HealthTab)"
      />
    </div>

    <!-- Library tab — the existing series-centric report, unchanged. -->
    <template v-if="activeTab === 'library'">
      <ErrorBanner v-if="healthError" :message="healthError" />
      <LibraryHealth
        :series="series"
        :loading="healthLoading"
        :refreshing="refreshing"
        @open-series="emit('open-series', $event)"
        @refresh="emit('refresh')"
      />
    </template>

    <!-- Sources tab — source-centric metrics (Kaizoku-grade report = slice 4). -->
    <SourceHealth
      v-else
      :metrics="metrics"
      :pending="sourcePending"
      :error="sourceError"
      :warming="warming"
      :warm-message="warmMessage"
      :warm-error="warmError"
      :resetting="resetting"
      :reset-error="resetError"
      @warm-now="emit('warm-now')"
      @reset-breaker="emit('reset-breaker', $event)"
    />
  </div>
</template>

<style scoped>
.health-console {
  min-height: 100%;
  background: var(--bg);
}

/* The tab bar sits above the per-tab screen. Horizontal padding matches the
 * screens' own side gutters; each tab screen brings its own top/bottom padding
 * (LibraryHealth / SourceHealth are unchanged), so the bar only pads its top. */
.health-console__tabs {
  padding: var(--space-2xl) var(--space-3xl) 0;
}

@media (max-width: 900px) {
  .health-console__tabs {
    padding: var(--space-lg) var(--space-lg) 0;
  }
}
</style>
