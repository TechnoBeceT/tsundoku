<script setup lang="ts">
import { useNow } from '../../composables/useNow'
import { eventTypeLabel } from '~/utils/eventType'
import { relativeTime, absoluteTime, formatDurationMs } from '~/utils/timeFormat'
import EventStatusBadge from './EventStatusBadge.vue'
import CategoryBadge from './CategoryBadge.vue'
import type { SourceEventRecord } from './sourceReport.types'

/**
 * EventTable — the raw audit-log feed as clickable rows: time (live-relative, the
 * exact instant on hover), optional source, operation, outcome, duration, and the
 * error category on a failure. Each row is a real `<button>` opening the forensic
 * detail (emits `select`). Presentation-only — the parent owns the page + filters
 * (the feed is server-paginated; this renders exactly the page it is given).
 *
 *   - `events` (required): the page of events to render.
 *   - `showSource` (default true): show the source column (hide in a per-source
 *     view where every row is the same source).
 *   - `pending` (default false): render skeleton rows while the page loads.
 *   - `emptyLabel` (default "No events match."): the empty message.
 */
withDefaults(defineProps<{
  /** The page of events. */
  events: SourceEventRecord[]
  /** Whether to show the source column. */
  showSource?: boolean
  /** Loading state — renders skeleton rows. */
  pending?: boolean
  /** Message when there are no events. */
  emptyLabel?: string
}>(), {
  showSource: true,
  pending: false,
  emptyLabel: 'No events match.',
})

const emit = defineEmits<{
  /** A row was clicked — open its forensic detail. */
  select: [event: SourceEventRecord]
}>()

const { now } = useNow()
const skeletons = [0, 1, 2, 3, 4]
</script>

<template>
  <div class="evt" :class="{ 'evt--no-source': !showSource }">
    <!-- Column header. -->
    <div class="evt__head" role="row">
      <span>Time</span>
      <span v-if="showSource">Source</span>
      <span>Operation</span>
      <span>Outcome</span>
      <span class="evt__num">Duration</span>
    </div>

    <!-- Loading skeletons. -->
    <template v-if="pending">
      <div v-for="n in skeletons" :key="n" class="evt__skeleton" />
    </template>

    <!-- Empty. -->
    <p v-else-if="events.length === 0" class="evt__empty">{{ emptyLabel }}</p>

    <!-- Rows. -->
    <template v-else>
      <button
        v-for="e in events"
        :key="e.id"
        type="button"
        class="evt__row"
        :class="{ 'evt__row--failed': e.status === 'failed' }"
        @click="emit('select', e)"
      >
        <span class="evt__time" :title="absoluteTime(e.createdAt)">{{ relativeTime(e.createdAt, now) }}</span>
        <span v-if="showSource" class="evt__source" :title="e.sourceName">{{ e.sourceName }}</span>
        <span class="evt__type">{{ eventTypeLabel(e.eventType) }}</span>
        <span class="evt__outcome">
          <EventStatusBadge :status="e.status" />
          <CategoryBadge v-if="e.status === 'failed'" :category="e.errorCategory" />
        </span>
        <span class="evt__num evt__dur">{{ formatDurationMs(e.durationMs) }}</span>
      </button>
    </template>
  </div>
</template>

<style scoped>
.evt {
  display: flex;
  flex-direction: column;
}

/* Shared column grid — header + rows read from the same template so they align.
 * Columns: time | source | operation | outcome | duration. */
.evt__head,
.evt__row {
  display: grid;
  grid-template-columns: 5.5rem minmax(0, 1fr) 6rem minmax(0, auto) 5rem;
  align-items: center;
  gap: 10px;
}

.evt--no-source .evt__head,
.evt--no-source .evt__row {
  grid-template-columns: 5.5rem 6rem minmax(0, auto) 5rem;
}

.evt__head {
  padding: 6px 10px;
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--faint);
  border-bottom: 1px solid var(--border);
}

.evt__row {
  padding: 9px 10px;
  border: none;
  border-bottom: 1px solid var(--border);
  background: none;
  font-family: var(--font-sans);
  text-align: left;
  cursor: pointer;
  transition: background 0.12s;
}

.evt__row:hover {
  background: var(--surface2);
}

.evt__row:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
  border-radius: var(--radius-sm);
}

/* A failed row carries a faint rose left rule — the eye finds failures fast. */
.evt__row--failed {
  box-shadow: inset 2px 0 0 var(--danger);
}

.evt__time {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
  white-space: nowrap;
}

.evt__source {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.evt__type {
  font-size: var(--text-sm);
  color: var(--muted);
  white-space: nowrap;
}

.evt__outcome {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.evt__num {
  text-align: right;
}

.evt__dur {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
}

.evt__empty {
  margin: 0;
  padding: 20px 10px;
  font-size: var(--text-sm);
  color: var(--muted);
}

.evt__skeleton {
  height: 38px;
  border-bottom: 1px solid var(--border);
  background: linear-gradient(90deg, transparent, var(--surface2), transparent);
  background-size: 200% 100%;
  animation: evt-shimmer 1.4s ease-in-out infinite;
}

@keyframes evt-shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

/* On a phone the operation + duration columns fold away; time · source · outcome
 * carry the row, with the rest available in the forensic detail on tap. */
@media (max-width: 640px) {
  .evt__head,
  .evt__row {
    grid-template-columns: 4.5rem minmax(0, 1fr) minmax(0, auto);
  }

  .evt--no-source .evt__head,
  .evt--no-source .evt__row {
    grid-template-columns: 4.5rem minmax(0, 1fr) minmax(0, auto);
  }

  .evt__type,
  .evt__num {
    display: none;
  }
}
</style>
