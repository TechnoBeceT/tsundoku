<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'
import FormError from '../ui/FormError.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import SourceMetricRow from './SourceMetricRow.vue'
import type { SourceMetric } from '../screens/settings.types'

/**
 * SourceMetricsPane — the Source Metrics pane: per-source search-performance
 * rows (slowest-first, as the backend returns them) plus a header "Warm now"
 * action that kicks a manual warm-up pass across anti-bot sources whose sessions
 * have gone cold. Presentation-only: the screen owns the data (useSourceMetrics),
 * this pane receives it via props and emits `warm-now` — it never fetches itself.
 *
 * The four §16 states are all visible: the pane shows its own loading skeletons
 * while `pending`, an inline `error` when the load failed, an empty state when
 * there are no metrics yet, and the Warm-now action drives `warming` (spinner) →
 * `warmMessage` (success) / `warmError` (failure).
 *
 *   - `metrics`: the per-source metric rows.
 *   - `pending`: the metrics list is loading.
 *   - `error`: the metrics load failed (inline message).
 *   - `warming`: a warm-up pass is in flight.
 *   - `warmMessage`: the last warm-up's success note (the warmed count).
 *   - `warmError`: the last warm-up's failure message.
 *   - `resetting`: the source id whose breaker reset is in flight (spins its row).
 *   - `resetError`: the last breaker-reset failure message (surfaced inline).
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
  /** The last warm-up's success note (e.g. "Warmed 12 sources"). */
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

// A few skeleton rows while the metrics list loads.
const skeletons = [0, 1, 2, 3]
</script>

<template>
  <SurfaceCard
    title="Source Metrics"
    sub="Per-source search latency + reliability. Anti-bot sources are slow only on a cold session — Warm now kicks a warm-up pass."
  >
    <template #actions>
      <AppButton variant="mini" size="sm" :loading="warming" @click="emit('warm-now')">
        <template #icon>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M8.5 14.5A2.5 2.5 0 0 0 11 12c0-1.38-.5-2-1-3-1.072-2.143-.224-4.054 2-6 .5 2.5 2 4.9 4 6.5 2 1.6 3 3.5 3 5.5a7 7 0 1 1-14 0c0-1.153.433-2.294 1-3a2.5 2.5 0 0 0 2.5 2.5z" /></svg>
        </template>
        Warm now
      </AppButton>
    </template>

    <!-- §16 warm-up result: success note or failure. -->
    <p v-if="warmMessage" class="warm-note">{{ warmMessage }}</p>
    <div v-if="warmError" class="warm-error">
      <FormError :message="warmError" />
    </div>

    <!-- §16 breaker-reset failure (success is reflected by the row's cleared state). -->
    <div v-if="resetError" class="warm-error">
      <FormError :message="resetError" />
    </div>

    <!-- Loading skeletons -->
    <div v-if="pending" class="metric-list">
      <div v-for="n in skeletons" :key="n" class="skeleton-row" />
    </div>

    <!-- Load error -->
    <div v-else-if="error" class="load-error">
      <FormError :message="error" />
    </div>

    <!-- Empty state -->
    <p v-else-if="metrics.length === 0" class="metric-empty">
      No source metrics yet — run a search or warm sources.
    </p>

    <!-- The metric rows -->
    <div v-else class="metric-list">
      <SourceMetricRow
        v-for="m in metrics"
        :key="m.id"
        :source="m"
        :resetting="resetting === m.id"
        @reset="emit('reset-breaker', $event)"
      />
    </div>
  </SurfaceCard>
</template>

<style scoped>
.metric-list {
  display: flex;
  flex-direction: column;
  gap: 9px;
}

/* Warm-up success note — quiet emerald confirmation above the list (§16). */
.warm-note {
  margin: 0 0 12px;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--set-ok-text);
}

.warm-error {
  margin-bottom: 12px;
}

.load-error {
  margin-top: 4px;
}

.metric-empty {
  padding: 14px 2px;
  font-size: var(--text-sm);
  color: var(--muted);
}

/* ---- Loading skeletons ---------------------------------------------------- */
.skeleton-row {
  height: 62px;
  border-radius: var(--radius-lg);
  background: var(--surface2);
  position: relative;
  overflow: hidden;
}

.skeleton-row::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: metric-shimmer 1.4s ease-in-out infinite;
}

@keyframes metric-shimmer {
  to { transform: translateX(100%); }
}
</style>
