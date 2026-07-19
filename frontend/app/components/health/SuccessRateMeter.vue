<script setup lang="ts">
import { computed } from 'vue'

/**
 * SuccessRateMeter — a source's success rate as a percentage + a threshold-tinted
 * bar. The tint encodes health without relying on the number alone: emerald ≥95%,
 * amber ≥80%, rose below (the report's good/warn/critical status ramp). Optional
 * `success`/`total` counts render as a quiet "1,147 / 1,284" detail.
 *
 *   - `rate` (required, 0..1): the success fraction (as the API returns it).
 *   - `success` / `total`: optional raw counts for the detail line.
 *   - `compact` (default false): drop the counts + shrink for a dense row.
 */
const props = withDefaults(defineProps<{
  /** Success fraction 0..1 (the API's `successRate`). */
  rate: number
  /** Optional successful-count for the detail line. */
  success?: number
  /** Optional total-count for the detail line. */
  total?: number
  /** Dense form: percentage + bar only, no counts. */
  compact?: boolean
}>(), {
  success: undefined,
  total: undefined,
  compact: false,
})

const pct = computed(() => Math.round(Math.min(1, Math.max(0, props.rate)) * 100))

// Threshold tone — the report's status ramp (never colour alone: the % is beside it).
const tone = computed(() => {
  if (props.rate >= 0.95) return 'var(--set-ok-dot)'
  if (props.rate >= 0.8) return 'var(--set-update-text)'
  return 'var(--danger)'
})

const detail = computed(() => {
  if (props.compact || props.total == null || props.success == null) return ''
  return `${props.success.toLocaleString()} / ${props.total.toLocaleString()}`
})
</script>

<template>
  <div class="rate" :class="{ 'rate--compact': compact }">
    <div class="rate__head">
      <span class="rate__pct" :style="{ color: tone }">{{ pct }}%</span>
      <span v-if="detail" class="rate__detail">{{ detail }}</span>
    </div>
    <span class="rate__track">
      <span class="rate__fill" :style="{ width: `${pct}%`, background: tone }" />
    </span>
  </div>
</template>

<style scoped>
.rate {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.rate__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.rate__pct {
  font-family: var(--font-mono);
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
}

.rate--compact .rate__pct {
  font-size: var(--text-sm);
}

.rate__detail {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
  white-space: nowrap;
}

.rate__track {
  height: 6px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  overflow: hidden;
}

.rate__fill {
  display: block;
  height: 100%;
  border-radius: var(--radius-pill);
  transition: width 0.3s ease;
}
</style>
