<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import SelectField from '../ui/SelectField.vue'
import { EVENT_TYPE_FILTER_OPTIONS, STATUS_FILTER_OPTIONS } from '~/utils/eventType'

/**
 * EventLogFilters — the event-log toolbar: an outcome filter, an operation-type
 * filter, and the pager (prev/next + "page X of Y" + total match count). The feed
 * is filtered + paginated SERVER-side, so this only emits the owner's choices; the
 * parent composable refetches. Presentation-only.
 *
 *   - `status` / `eventType`: the current filter values ('' = any).
 *   - `page` (0-based) / `pageCount` / `total`: the pager state.
 *   - `pending` (default false): disables the pager while a page loads.
 */
const props = withDefaults(defineProps<{
  /** Current outcome filter ('' = any). */
  status: string
  /** Current operation-type filter ('' = any). */
  eventType: string
  /** Current 0-based page. */
  page: number
  /** Total number of pages (≥1). */
  pageCount: number
  /** Total matching event count (for the "N events" readout). */
  total: number
  /** Whether a page load is in flight. */
  pending?: boolean
}>(), {
  pending: false,
})

const emit = defineEmits<{
  /** Outcome filter changed. */
  'update:status': [value: string]
  /** Operation-type filter changed. */
  'update:eventType': [value: string]
  /** Go to the previous page. */
  'prev': []
  /** Go to the next page. */
  'next': []
}>()

const statusOptions = STATUS_FILTER_OPTIONS
const typeOptions = EVENT_TYPE_FILTER_OPTIONS

const atStart = computed(() => props.page <= 0)
const atEnd = computed(() => props.page >= props.pageCount - 1)
</script>

<template>
  <div class="filters">
    <div class="filters__selects">
      <SelectField
        :model-value="status"
        :options="statusOptions"
        aria-label="Filter by outcome"
        @update:model-value="emit('update:status', $event)"
      />
      <SelectField
        :model-value="eventType"
        :options="typeOptions"
        aria-label="Filter by operation type"
        @update:model-value="emit('update:eventType', $event)"
      />
    </div>

    <div class="filters__pager">
      <span class="filters__total">{{ total.toLocaleString() }} events</span>
      <AppButton variant="ghost" size="sm" :disabled="pending || atStart" @click="emit('prev')">Prev</AppButton>
      <span class="filters__page">{{ page + 1 }} / {{ pageCount }}</span>
      <AppButton variant="ghost" size="sm" :disabled="pending || atEnd" @click="emit('next')">Next</AppButton>
    </div>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.filters__selects {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.filters__pager {
  display: flex;
  align-items: center;
  gap: 8px;
}

.filters__total {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
  margin-right: 4px;
  white-space: nowrap;
}

.filters__page {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--muted);
  white-space: nowrap;
  min-width: 3.5rem;
  text-align: center;
}
</style>
