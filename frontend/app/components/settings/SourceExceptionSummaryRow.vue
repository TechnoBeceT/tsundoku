<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { components } from '../../utils/api/schema.d.ts'

type SourceIdentity = components['schemas']['SourceIdentity']
type SourceRuntimeStatus = components['schemas']['SourceRuntimeStatus']

/**
 * One semantic button in the source overview rail. A highlighted row is an
 * external navigation target: it receives keyboard focus and is scrolled into
 * view without overriding reduced-motion preferences.
 */
const props = withDefaults(defineProps<{
  source: SourceIdentity
  exceptionCount?: number
  runtime?: SourceRuntimeStatus | null
  selected?: boolean
  highlighted?: boolean
}>(), {
  exceptionCount: 0,
  runtime: null,
  selected: false,
  highlighted: false,
})

const emit = defineEmits<{
  select: [sourceId: string]
}>()

const row = ref<HTMLButtonElement | null>(null)
const exceptionLabel = computed(() => props.exceptionCount === 0
  ? 'Inherits all settings'
  : `${props.exceptionCount} ${props.exceptionCount === 1 ? 'override' : 'overrides'}`)
const statusLabel = computed(() => {
  if (!props.runtime) return 'Catalog source'
  if (props.runtime.status === 'pending' && props.runtime.lastApplyError) return 'Apply needs attention'
  return props.runtime.status === 'pending' ? 'Apply pending' : 'Applied'
})

watch(() => props.highlighted, async (highlighted) => {
  if (!highlighted) return
  await nextTick()
  row.value?.focus({ preventScroll: true })
  const reducedMotion = globalThis.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  row.value?.scrollIntoView?.({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'nearest' })
}, { immediate: true })
</script>

<template>
  <button
    ref="row"
    type="button"
    class="summary-row"
    :class="{
      'summary-row--selected': selected,
      'summary-row--highlighted': highlighted,
      'summary-row--exception': exceptionCount > 0,
    }"
    :aria-current="selected ? 'true' : undefined"
    :data-highlighted="highlighted ? 'true' : undefined"
    @click="emit('select', source.sourceId)"
  >
    <span class="summary-row__identity">
      <span class="summary-row__name">{{ source.name }}</span>
      <span class="summary-row__language">{{ source.language }}</span>
    </span>
    <span class="summary-row__meta">
      <span class="summary-row__exceptions">{{ exceptionLabel }}</span>
      <span
        class="summary-row__status"
        :class="{
          'summary-row__status--pending': runtime?.status === 'pending',
          'summary-row__status--error': Boolean(runtime?.lastApplyError),
        }"
      >
        {{ statusLabel }}
      </span>
    </span>
  </button>
</template>

<style scoped>
.summary-row {
  display: grid;
  gap: var(--space-xs-tight);
  width: 100%;
  min-width: 0;
  padding: var(--space-sm) var(--space-base);
  border: 0;
  border-left: 3px solid transparent;
  border-radius: var(--radius-md);
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.summary-row:hover {
  background: var(--surface2);
}

.summary-row:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.summary-row--exception {
  border-left-color: var(--border2);
}

.summary-row--selected {
  border-left-color: var(--accent);
  background: var(--accentSoft);
}

.summary-row--highlighted:not(.summary-row--selected) {
  border-left-color: var(--accentBright);
  background: var(--surface2);
}

.summary-row__identity,
.summary-row__meta {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-xs);
  min-width: 0;
}

.summary-row__name {
  min-width: 0;
  color: var(--text);
  font-weight: var(--weight-bold);
  overflow-wrap: anywhere;
}

.summary-row__language,
.summary-row__status {
  flex: none;
  color: var(--faint);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.summary-row__exceptions {
  min-width: 0;
  color: var(--muted);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.summary-row--exception .summary-row__exceptions,
.summary-row__status--pending {
  color: var(--accentBright);
}

.summary-row__status--error {
  color: var(--danger-text);
}

@media (max-width: 420px) {
  .summary-row__meta {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-3xs);
  }
}
</style>
