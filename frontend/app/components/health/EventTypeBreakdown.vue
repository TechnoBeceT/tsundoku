<script setup lang="ts">
import { computed } from 'vue'
import { eventTypeLabel } from '~/utils/eventType'
import type { EventTypeTally } from './sourceReport.types'

/**
 * EventTypeBreakdown — per-operation success/fail split: one row per event type
 * (search / download / refresh / …) with a two-tone bar (emerald success, rose
 * failure) sized by the type's share of activity, plus the exact counts. Answers
 * "search works but downloads fail" at a glance. Pure CSS, zero chart dep.
 *
 *   - `items` (required): the per-type tallies (any order; sorted by total here).
 *   - `emptyLabel` (default "No operations in this window").
 */
const props = withDefaults(defineProps<{
  /** Per-operation tallies. */
  items: EventTypeTally[]
  /** Message when empty. */
  emptyLabel?: string
}>(), {
  emptyLabel: 'No operations in this window',
})

// Sort by total desc so the busiest operation leads; the bar splits by outcome.
const rows = computed(() =>
  [...props.items]
    .filter(i => i.total > 0)
    .sort((a, b) => b.total - a.total)
    .map(i => ({
      key: i.eventType,
      label: eventTypeLabel(i.eventType),
      total: i.total,
      success: i.success,
      failed: i.failed,
      successPct: i.total > 0 ? (i.success / i.total) * 100 : 0,
    })))
</script>

<template>
  <div class="etb">
    <p v-if="rows.length === 0" class="etb__empty">{{ emptyLabel }}</p>
    <div v-for="row in rows" v-else :key="row.key" class="etb__row">
      <div class="etb__head">
        <span class="etb__label">{{ row.label }}</span>
        <span class="etb__counts">
          <span class="etb__ok">{{ row.success.toLocaleString() }}</span>
          <span v-if="row.failed > 0" class="etb__fail">· {{ row.failed.toLocaleString() }} failed</span>
        </span>
      </div>
      <span class="etb__track" :title="`${row.success} ok / ${row.failed} failed of ${row.total}`">
        <span class="etb__ok-fill" :style="{ width: `${row.successPct}%` }" />
      </span>
    </div>
  </div>
</template>

<style scoped>
.etb {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.etb__empty {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.etb__row {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.etb__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
}

.etb__label {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.etb__counts {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.etb__ok { color: var(--set-ok-text); font-weight: var(--weight-bold); }
.etb__fail { color: var(--danger-text); }

/* The full track is the rose "failed" bed; the emerald fill is the success share,
 * so the remaining red tail is the failure proportion — two series, one bar. */
.etb__track {
  height: 7px;
  border-radius: var(--radius-pill);
  background: var(--danger-bg);
  overflow: hidden;
}

.etb__ok-fill {
  display: block;
  height: 100%;
  border-radius: var(--radius-pill);
  background: var(--set-ok-dot);
  transition: width 0.3s ease;
}
</style>
