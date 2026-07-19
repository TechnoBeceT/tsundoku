<script setup lang="ts">
import SourceMetricsPane from '../health/SourceMetricsPane.vue'
import type { SourceMetric } from './sourceHealth.types'

/**
 * SourceHealth — the "Sources" tab of the `/health` console: source-centric
 * search-performance + anti-ban health, in contrast to the sibling
 * LibraryHealth tab's series-centric view.
 *
 * For slice 3 this hosts the RELOCATED source-metrics UI (moved off the Settings
 * screen): the per-source metric rows, the "Warm now" pass, warm/cold + isSlow
 * badges, and the circuit-breaker cooldown banner + Reset. The Kaizoku-grade
 * report sections (KPI cards, per-source timeline histogram, event log) are
 * slice 4 — they stack ABOVE the metrics list at the marked mount point below.
 *
 * Presentation only (props-down/events-up, like LibraryHealth/Settings): the
 * composition root (`pages/health.vue`) owns the data via useSourceMetrics and
 * every §16 state; this screen receives them and forwards the two actions
 * (`warm-now`, `reset-breaker`) as emits — it never fetches. Token-only colours
 * → both themes.
 *
 *   - `metrics`: the per-source metric rows (slowest-first).
 *   - `pending`: the metrics list is loading.
 *   - `error`: a metrics-load failure (surfaced inline in the pane).
 *   - `warming` / `warmMessage` / `warmError`: §16 state of the Warm-now action.
 *   - `resetting` / `resetError`: §16 state of the per-source breaker Reset.
 */
withDefaults(defineProps<{
  /** The per-source metric rows (slowest-first). */
  metrics: SourceMetric[]
  /** Whether the metrics list is loading. */
  pending?: boolean
  /** A metrics-load failure, surfaced inline. */
  error?: string | null
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
  pending: false,
  error: null,
  warming: false,
  warmMessage: null,
  warmError: null,
  resetting: null,
  resetError: null,
})

const emit = defineEmits<{
  /** Trigger a manual warm-up pass across all sources. */
  'warm-now': []
  /** Reset a source's tripped circuit-breaker — carries the source id. */
  'reset-breaker': [id: string]
}>()
</script>

<template>
  <div class="source-health">
    <!--
      SLICE 4 MOUNT POINT — the Kaizoku-grade report sections stack HERE, above
      the metrics list (KPI StatCards → leaderboards → recent-errors →
      per-source accordion → event log). They consume the slice-2 reporting API
      (GET /api/reporting/*) via new composables (useSourceReport/Events/Timeline)
      and are deliberately NOT built in slice 3 — this is only the tab shell +
      the relocated metrics pane.
    -->

    <SourceMetricsPane
      :metrics="metrics"
      :pending="pending"
      :error="error"
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
/* GROW screen (QCAT-265): the document scrolls, the content grows. Padding
 * matches LibraryHealth's own so both tabs feel consistent; `--app-nav-bottom`
 * (0 on desktop) clears the phone bottom-nav. */
.source-health {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

@media (max-width: 900px) {
  .source-health {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
