<script setup lang="ts">
import { computed } from 'vue'
import ProgressBar from '../ui/ProgressBar.vue'

/**
 * ScanProgress — the Scan Library wizard's live progress readout, driven by
 * the backend's `scan.progress` SSE frames (see `useScanLibrary`'s
 * `ScanState`). The walk doesn't know how many series exist on disk until it
 * has listed them, so the bar stays INDETERMINATE (a sliding segment, no
 * count) until a `total` is known — a determinate percentage before that
 * point would just be a guess dressed up as a number.
 *
 *   - `processed` (default 0): series scanned so far.
 *   - `total` (default 0): series discovered so far; 0 = not yet known.
 */
const props = withDefaults(defineProps<{
  /** Series scanned so far. */
  processed?: number
  /** Series discovered so far; 0 means not yet known. */
  total?: number
}>(), {
  processed: 0,
  total: 0,
})

const known = computed(() => props.total > 0)
const pct = computed(() => (known.value ? Math.min(100, Math.round((props.processed / props.total) * 100)) : 0))
const label = computed(() => (known.value ? `${props.processed} / ${props.total}` : 'Scanning…'))
</script>

<template>
  <div class="scan-progress">
    <ProgressBar class="scan-progress__bar" :value="known ? pct : undefined" />
    <span class="scan-progress__label">{{ label }}</span>
  </div>
</template>

<style scoped>
.scan-progress {
  display: flex;
  align-items: center;
  gap: 12px;
}

.scan-progress__bar {
  flex: 1;
}

.scan-progress__label {
  flex: none;
  min-width: 84px;
  text-align: right;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
</style>
