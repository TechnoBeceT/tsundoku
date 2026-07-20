<script setup lang="ts">
import { computed } from 'vue'

/**
 * DownloadCounter — the always-visible download-activity trio pinned at the foot
 * of the AppShell nav rail: three colour-coded counts stacked vertically —
 * DOWNLOADING (blue), QUEUED (yellow), FAILED (red). It is persistent (shown on
 * every page) so the owner always knows the queue is *waiting*, not empty, and it
 * links to the Downloads screen.
 *
 * Each count stays visible even at 0 (dimmed) so the trio never collapses/reflows;
 * a non-zero count brightens + a live pulse marks activity. The whole cluster is
 * one button — clicking it emits `navigate` (the parent routes to Downloads).
 *
 * Presentation only: counts in, one emit out. Token-only so both themes read.
 */
const props = withDefaults(defineProps<{
  /** Chapters currently fetching — the blue count. */
  downloading?: number
  /** Chapters waiting to download — the yellow count. */
  queued?: number
  /** Chapters needing attention (failed/terminal) — the red count. */
  failed?: number
}>(), {
  downloading: 0,
  queued: 0,
  failed: 0,
})

const emit = defineEmits<{
  /** The counter was clicked — the parent navigates to the Downloads screen. */
  navigate: []
}>()

const rows = computed(() => [
  { key: 'downloading', tone: 'active', value: props.downloading, label: 'downloading' },
  { key: 'queued', tone: 'queued', value: props.queued, label: 'queued' },
  { key: 'failed', tone: 'failed', value: props.failed, label: 'failed' },
] as const)

// One combined tooltip / a11y label so the compact trio reads for screen readers.
const summary = computed(() =>
  `${props.downloading} downloading, ${props.queued} queued, ${props.failed} failed`,
)
</script>

<template>
  <button
    type="button"
    class="counter"
    :title="`Downloads — ${summary}`"
    :aria-label="`Downloads: ${summary}`"
    @click="emit('navigate')"
  >
    <span
      v-for="row in rows"
      :key="row.key"
      class="counter__row"
      :class="[`counter__row--${row.tone}`, { 'counter__row--zero': row.value === 0 }]"
    >
      <span class="counter__dot" :class="{ 'counter__dot--live': row.value > 0 }" aria-hidden="true" />
      <span class="counter__num">{{ row.value }}</span>
    </span>
  </button>
</template>

<style scoped>
.counter {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 3px;
  width: 42px;
  padding: 6px 0;
  border: none;
  border-radius: var(--radius-lg);
  background: transparent;
  cursor: pointer;
  transition: background 0.15s;
}

.counter:hover {
  background: var(--surface2);
}

.counter:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.counter__row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}

/* A zero count is present but muted — the trio never collapses/reflows. */
.counter__row--zero {
  opacity: 0.4;
}

.counter__row--active { color: var(--accentBright); }
.counter__row--queued { color: var(--warn); }
.counter__row--failed { color: var(--danger); }

.counter__dot {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: var(--radius-pill);
  background: currentColor;
}

/* A live (non-zero) count pulses so activity draws the eye. */
.counter__dot--live {
  animation: pulseO 1.5s ease-in-out infinite;
}

.counter__num {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  font-variant-numeric: tabular-nums;
  min-width: 1ch;
  text-align: left;
}
</style>
