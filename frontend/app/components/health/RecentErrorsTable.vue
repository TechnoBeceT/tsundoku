<script setup lang="ts">
import { useNow } from '../../composables/useNow'
import { relativeTime, absoluteTime } from '~/utils/timeFormat'
import CategoryBadge from './CategoryBadge.vue'
import type { SourceEventRecord } from './sourceReport.types'

/**
 * RecentErrorsTable — the overview's "what just broke" preview: the most recent
 * failed events, newest first, as compact clickable rows (source · category ·
 * truncated message · when). Each row opens the forensic detail (emits `select`).
 * A tighter cousin of `EventTable` for the always-visible summary; the full log
 * lives lower on the page.
 *
 *   - `errors` (required): the recent failed events (already newest-first).
 *   - `emptyLabel` (default "No errors — every source is behaving.").
 */
withDefaults(defineProps<{
  /** The recent failed events. */
  errors: SourceEventRecord[]
  /** Message when there are none (the good state). */
  emptyLabel?: string
}>(), {
  emptyLabel: 'No errors — every source is behaving.',
})

const emit = defineEmits<{
  /** A row was clicked — open its forensic detail. */
  select: [event: SourceEventRecord]
}>()

const { now } = useNow()
</script>

<template>
  <div class="rec">
    <p v-if="errors.length === 0" class="rec__empty">{{ emptyLabel }}</p>
    <button
      v-for="e in errors"
      v-else
      :key="e.id"
      type="button"
      class="rec__row"
      @click="emit('select', e)"
    >
      <div class="rec__top">
        <span class="rec__source">{{ e.sourceName }}</span>
        <CategoryBadge :category="e.errorCategory" />
        <span class="rec__time" :title="absoluteTime(e.createdAt)">{{ relativeTime(e.createdAt, now) }}</span>
      </div>
      <p class="rec__msg" :title="e.errorMessage ?? ''">{{ e.errorMessage || 'No message' }}</p>
    </button>
  </div>
</template>

<style scoped>
.rec {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rec__empty {
  margin: 0;
  padding: 6px 0;
  font-size: var(--text-sm);
  color: var(--set-ok-text);
  font-weight: var(--weight-semibold);
}

.rec__row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface2);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.12s, background 0.12s;
  /* A faint rose left rule marks each row as a failure. */
  box-shadow: inset 2px 0 0 var(--danger);
}

.rec__row:hover {
  border-color: var(--border2);
  background: var(--surface3);
}

.rec__row:focus-visible {
  outline: none;
  box-shadow: inset 2px 0 0 var(--danger), var(--ring-focus);
}

.rec__top {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rec__source {
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.rec__time {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
  white-space: nowrap;
}

.rec__msg {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--danger-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
