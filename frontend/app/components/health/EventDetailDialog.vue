<script setup lang="ts">
import { computed } from 'vue'
import Dialog from '../ui/Dialog.vue'
import { useErrorDiagnosis } from '../../composables/useErrorDiagnosis'
import { eventTypeLabel } from '~/utils/eventType'
import { absoluteTime, formatDurationMs } from '~/utils/timeFormat'
import EventStatusBadge from './EventStatusBadge.vue'
import CategoryBadge from './CategoryBadge.vue'
import type { SourceEventRecord } from './sourceReport.types'

/**
 * EventDetailDialog — the single-event forensic modal. Composes `ui/Dialog` and
 * lays out one audit-log event in full: identity + timing + outcome, the metadata
 * context (keyword / url / series / chapter), and — for a failure — the human
 * DIAGNOSIS (title, explanation, ordered suggestions from `useErrorDiagnosis`)
 * over the raw error text. This is the report's third altitude: "show me the
 * request that failed at 14:32, and tell me what to do about it."
 *
 *   - `open` (v-model:open): whether the modal is shown.
 *   - `event`: the event to inspect (null renders nothing — the parent clears it
 *     on close).
 */
const props = defineProps<{
  /** Whether the modal is open (v-model:open). */
  open: boolean
  /** The event to inspect; null when nothing is selected. */
  event: SourceEventRecord | null
}>()

const emit = defineEmits<{
  /** Open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The modal closed. */
  'close': []
}>()

const { diagnose } = useErrorDiagnosis()

const isFailed = computed(() => props.event?.status === 'failed')

// The diagnosis for a failed event (null for a success — no diagnosis needed).
const diagnosis = computed(() =>
  props.event && isFailed.value ? diagnose(props.event.errorCategory, props.event.errorMessage) : null)

// Metadata as ordered [key, value] pairs (the forensic context).
const metadataEntries = computed(() => Object.entries(props.event?.metadata ?? {}))

const title = computed(() => (props.event ? `${props.event.sourceName} · ${eventTypeLabel(props.event.eventType)}` : 'Event'))
</script>

<template>
  <Dialog
    :open="open"
    :title="title"
    max-width="560px"
    @update:open="emit('update:open', $event)"
    @close="emit('close')"
  >
    <div v-if="event" class="detail">
      <!-- Outcome + timing summary. -->
      <div class="detail__summary">
        <EventStatusBadge :status="event.status" />
        <CategoryBadge v-if="isFailed" :category="event.errorCategory" />
        <span class="detail__when">{{ absoluteTime(event.createdAt) }}</span>
      </div>

      <!-- Field grid. -->
      <dl class="detail__grid">
        <div class="detail__field">
          <dt>Duration</dt>
          <dd>{{ formatDurationMs(event.durationMs) }}</dd>
        </div>
        <div v-if="event.itemsCount != null" class="detail__field">
          <dt>Items</dt>
          <dd>{{ event.itemsCount.toLocaleString() }}</dd>
        </div>
        <div class="detail__field">
          <dt>Source key</dt>
          <dd class="detail__mono">{{ event.sourceKey }}</dd>
        </div>
        <div v-if="event.language" class="detail__field">
          <dt>Language</dt>
          <dd>{{ event.language }}</dd>
        </div>
      </dl>

      <!-- Diagnosis (failures only). -->
      <section v-if="diagnosis" class="detail__diag">
        <h3 class="detail__diag-title">{{ diagnosis.title }}</h3>
        <p class="detail__diag-body">{{ diagnosis.explanation }}</p>
        <p class="detail__diag-label">Try this</p>
        <ul class="detail__suggestions">
          <li v-for="(s, i) in diagnosis.suggestions" :key="i">{{ s }}</li>
        </ul>
      </section>

      <!-- Raw error text (failures only). -->
      <section v-if="event.errorMessage" class="detail__raw">
        <p class="detail__raw-label">Raw error</p>
        <pre class="detail__raw-text">{{ event.errorMessage }}</pre>
      </section>

      <!-- Metadata context. -->
      <section v-if="metadataEntries.length > 0" class="detail__meta">
        <p class="detail__meta-label">Context</p>
        <dl class="detail__meta-grid">
          <template v-for="[key, value] in metadataEntries" :key="key">
            <dt>{{ key }}</dt>
            <dd :title="value">{{ value }}</dd>
          </template>
        </dl>
      </section>
    </div>
  </Dialog>
</template>

<style scoped>
.detail {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.detail__summary {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.detail__when {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
}

.detail__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
  gap: 12px;
  margin: 0;
}

.detail__field dt {
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--faint);
}

.detail__field dd {
  margin: 2px 0 0;
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.detail__mono {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  word-break: break-all;
}

/* Diagnosis block — the actionable centre of the modal, on a tinted panel. */
.detail__diag {
  padding: 14px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.detail__diag-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.detail__diag-body {
  margin: 6px 0 0;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}

.detail__diag-label,
.detail__raw-label,
.detail__meta-label {
  margin: 12px 0 6px;
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--faint);
}

.detail__suggestions {
  margin: 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.detail__suggestions li {
  font-size: var(--text-base);
  line-height: 1.45;
  color: var(--text);
}

.detail__raw-text {
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--bg2);
  border: 1px solid var(--border);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--danger-text);
  white-space: pre-wrap;
  word-break: break-word;
}

.detail__meta-grid {
  display: grid;
  grid-template-columns: minmax(0, auto) minmax(0, 1fr);
  gap: 4px 14px;
  margin: 0;
}

.detail__meta-grid dt {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
}

.detail__meta-grid dd {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
