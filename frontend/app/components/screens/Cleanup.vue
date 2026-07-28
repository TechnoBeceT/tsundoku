<script setup lang="ts">
import ErrorBanner from '../ui/ErrorBanner.vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import Fractionals from './Fractionals.vue'
import Sourceless from './Sourceless.vue'
import Duplicates from './Duplicates.vue'
import { CLEANUP_TABS, type CleanupTab } from '~/utils/cleanupTabs'
import type {
  CleanupFractionalsPane,
  CleanupSourcelessPane,
  CleanupDuplicatesPane,
} from './cleanup.types'

/**
 * Cleanup — the top-level `/cleanup` console: a 3-tab screen composing the three
 * library-wide cleanup surfaces that used to be three separate sidebar entries.
 *   - Fractionals → the existing Fractionals screen, REUSED unchanged.
 *   - Sourceless  → the existing Sourceless screen, REUSED unchanged.
 *   - Duplicates  → the new Duplicates screen (discovery only).
 *
 * Presentation only, mirroring the Health console exactly: the active tab is
 * CONTROLLED (the composition root `pages/cleanup.vue` owns it, persists it to
 * sessionStorage and reads the `?tab=` deep-link), and every child action is
 * forwarded up as an emit. This shell owns only the tab bar and which tab renders.
 *
 * 🔴 ONE TAB RENDERS AT A TIME, by v-if — not v-show. Each tab body is driven by
 * a library-wide scan the page fetches LAZILY on first reveal, so mounting all
 * three would defeat that: the page would pay for every scan the moment the
 * console opened. Token-only colours → both themes.
 */
withDefaults(defineProps<{
  /** Which tab is showing (controlled — the tab bar emits `set-tab`). */
  activeTab?: CleanupTab
  /** The Fractionals tab's data + §16 state. */
  fractionals: CleanupFractionalsPane
  /** The Sourceless tab's data + §16 state. */
  sourceless: CleanupSourcelessPane
  /** The Duplicates tab's data + §16 state. */
  duplicates: CleanupDuplicatesPane
}>(), {
  activeTab: 'fractionals',
})

const emit = defineEmits<{
  /** A tab was selected — carries its key. */
  'set-tab': [tab: CleanupTab]
  /** A card was clicked — open that series' detail view (any tab). */
  'open-series': [seriesId: string]
  /** Rescan the active tab's list. */
  'refresh': [tab: CleanupTab]
  /** A Fractionals card's ignore toggle flipped. */
  'toggle-ignore': [payload: { seriesId: string, ignore: boolean }]
  /** A Fractionals card's "Clean files" was clicked. */
  'clean-files': [seriesId: string]
  /** A Sourceless card's "Review" was clicked. */
  'review-sourceless': [seriesId: string]
}>()
</script>

<template>
  <div class="cleanup-console">
    <div class="cleanup-console__tabs">
      <SegmentedTabs
        :model-value="activeTab"
        :tabs="CLEANUP_TABS"
        @update:model-value="emit('set-tab', $event as CleanupTab)"
      />
    </div>

    <!-- Fractionals tab — the existing screen, unchanged. -->
    <template v-if="activeTab === 'fractionals'">
      <ErrorBanner v-if="fractionals.error" :message="fractionals.error" />
      <ErrorBanner v-if="fractionals.toggleError" :message="fractionals.toggleError" />
      <Fractionals
        :series="fractionals.series"
        :loading="fractionals.loading"
        :refreshing="fractionals.refreshing"
        :busy-ids="fractionals.busyIds"
        @open-series="emit('open-series', $event)"
        @toggle-ignore="emit('toggle-ignore', $event)"
        @clean-files="emit('clean-files', $event)"
        @refresh="emit('refresh', 'fractionals')"
      />
    </template>

    <!-- Sourceless tab — the existing screen, unchanged. -->
    <template v-else-if="activeTab === 'sourceless'">
      <ErrorBanner v-if="sourceless.error" :message="sourceless.error" />
      <Sourceless
        :series="sourceless.series"
        :loading="sourceless.loading"
        :refreshing="sourceless.refreshing"
        @review="emit('review-sourceless', $event)"
        @refresh="emit('refresh', 'sourceless')"
      />
    </template>

    <!-- Duplicates tab — discovery only; each row opens its series. -->
    <template v-else>
      <ErrorBanner v-if="duplicates.error" :message="duplicates.error" />
      <Duplicates
        :series="duplicates.series"
        :total-files="duplicates.totalFiles"
        :total-bytes="duplicates.totalBytes"
        :loading="duplicates.loading"
        :refreshing="duplicates.refreshing"
        @open-series="emit('open-series', $event)"
        @refresh="emit('refresh', 'duplicates')"
      />
    </template>
  </div>
</template>

<style scoped>
.cleanup-console {
  min-height: 100%;
  background: var(--bg);
}

/* The tab bar sits above the per-tab screen. Horizontal padding matches the
 * screens' own side gutters; each tab screen brings its own top/bottom padding
 * (Fractionals / Sourceless / Duplicates are unchanged), so the bar only pads
 * its top. Mirrors the Health console's tab bar exactly. */
.cleanup-console__tabs {
  padding: var(--space-2xl) var(--space-3xl) 0;
}

@media (max-width: 900px) {
  .cleanup-console__tabs {
    padding: var(--space-lg) var(--space-lg) 0;
  }
}
</style>
