<script setup lang="ts">
import { computed } from 'vue'
import type { ChapterState } from './types'

/**
 * StatusBadge — a labelled pill + status dot for a chapter's download state.
 *
 * Renders the human label for the given `Chapter.state` and tints itself from
 * the canonical `--state-<state>-{fg,bg,dot}` tokens in `tokens/status.css`
 * (theme-independent), so every chapter-state badge across the app reads from
 * one palette. The dot is the solid status colour; the pill is the soft tint
 * with the foreground text colour.
 *
 *   - `state` (required): one of the eight `ChapterState` values.
 */
const props = defineProps<{
  /** The chapter state to render — drives both the label and the colour. */
  state: ChapterState
}>()

// Human-facing label per state — wording matches the prototype's badge maps
// (SeriesDetail.vue / Downloads.vue).
const LABELS: Record<ChapterState, string> = {
  wanted: 'Wanted',
  downloading: 'Downloading',
  downloaded: 'On disk',
  upgrade_available: 'Upgrade ready',
  upgrading: 'Upgrading',
  failed: 'Failed',
  permanently_failed: 'Failed · final',
  superseded: 'Superseded',
  ignored: 'Ignored',
}

const label = computed(() => LABELS[props.state])

// Local custom-props point at the per-state token triple — the class then
// reads them, so the colour wiring lives once (no per-state CSS rule).
const vars = computed(() => ({
  '--badge-fg': `var(--state-${props.state}-fg)`,
  '--badge-bg': `var(--state-${props.state}-bg)`,
  '--badge-dot': `var(--state-${props.state}-dot)`,
}))
</script>

<template>
  <span class="badge" :style="vars">
    <span class="badge__dot" aria-hidden="true" />
    {{ label }}
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
  color: var(--badge-fg);
  background: var(--badge-bg);
}

.badge__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-pill);
  flex-shrink: 0;
  background: var(--badge-dot);
}
</style>
