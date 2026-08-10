<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '../ui/Spinner.vue'
import FormError from '../ui/FormError.vue'
import EmptyState from '../ui/EmptyState.vue'
import Tag from '../ui/Tag.vue'
import type { SourceSeriesRow } from './sourceSeries.types'

/**
 * SourceSeriesPanel — the read-only "what depends on this source" list (QCAT-513).
 * Presentation-only: the owning pane fetches via useSourceSeries and passes the
 * rows + §16 state down; this panel never fetches itself.
 *
 * It shows all four states explicitly (§16): a loading row while `pending`, an
 * inline `error` when the load failed, an EmptyState when nothing carries the
 * source, and — on success — a summary line ("N series · M go dark") over one row
 * per series. Each row is either a DANGER "Goes dark" Tag (the source is its only
 * provider — pausing leaves it with nothing to fetch new chapters) or the
 * take-over provider that keeps feeding it. Downloaded chapters stay on disk
 * either way; this view is about FUTURE chapters only.
 *
 *   - `rows`: the dependent series (already mapped).
 *   - `pending`: a load is in flight.
 *   - `error`: a load failure message (or null).
 */
const props = withDefaults(defineProps<{
  /** The dependent series carrying this source. */
  rows?: SourceSeriesRow[]
  /** Whether the list is loading. */
  pending?: boolean
  /** A load failure, surfaced inline. */
  error?: string | null
}>(), {
  rows: () => [],
  pending: false,
  error: null,
})

// Headline counts — how many series carry the source and how many go dark.
const total = computed(() => props.rows.length)
const darkCount = computed(() => props.rows.filter(r => r.goesDark).length)

// The summary sentence above the list — a "none go dark" phrasing so the
// reassuring case reads cleanly instead of "0 go dark". ("series" is invariant.)
const summaryText = computed(() => {
  const darkPart = darkCount.value === 0 ? 'none go dark' : `${darkCount.value} go dark`
  return `${total.value} series · ${darkPart}`
})
</script>

<template>
  <div class="ss">
    <!-- §16 loading -->
    <div v-if="pending" class="ss__loading">
      <Spinner :size="16" tone="accent" />
      <span>Checking affected series…</span>
    </div>

    <!-- §16 error -->
    <div v-else-if="error" class="ss__error">
      <FormError :message="error" />
    </div>

    <!-- §16 empty -->
    <EmptyState
      v-else-if="rows.length === 0"
      title="Nothing depends on this source"
      sub="No adopted series carry it, so pausing changes nothing."
    />

    <!-- §16 success -->
    <template v-else>
      <p class="ss__summary">{{ summaryText }}</p>
      <ul class="ss__list">
        <li v-for="row in rows" :key="row.seriesId" class="ss__row">
          <span class="ss__title">{{ row.title }}</span>
          <Tag v-if="row.goesDark" tone="danger" size="sm">Goes dark</Tag>
          <span v-else class="ss__takeover">
            Taken over by <strong>{{ row.topAlternative }}</strong>
          </span>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.ss {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 4px 0 2px;
}

.ss__loading {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 2px;
  font-size: var(--text-sm);
  color: var(--muted);
}

.ss__error {
  padding: 4px 0;
}

.ss__summary {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--faint);
}

.ss__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}

.ss__row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 2px;
  border-top: 1px solid var(--border);
}

.ss__row:first-child {
  border-top: none;
}

.ss__title {
  flex: 1;
  min-width: 0;
  font-size: var(--text-sm);
  color: var(--text);
  overflow-wrap: anywhere;
}

.ss__takeover {
  flex: none;
  font-size: var(--text-xs);
  color: var(--muted);
  text-align: right;
}

.ss__takeover strong {
  color: var(--text);
  font-weight: var(--weight-semibold);
}
</style>
