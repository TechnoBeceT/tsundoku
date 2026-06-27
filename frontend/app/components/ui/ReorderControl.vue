<script setup lang="ts">
import type { MoveDirection } from './controls.types'

/**
 * ReorderControl — the up/down arrow stepper used to re-rank list items
 * (Series-Detail sources, Import candidates, Settings categories + repos). An up
 * arrow, an optional rank number, and a down arrow stacked vertically.
 *
 * `canUp` / `canDown` gate each arrow (the top item can't go up, the bottom
 * can't go down); `disabled` blocks both (e.g. while a save is in flight).
 * `rank` shows a position number between the arrows (hidden when undefined);
 * `topHighlighted` paints the rank-1 number in the accent. Emits `move` with
 * `-1` (up) or `1` (down).
 */
withDefaults(defineProps<{
  /** Whether the up arrow is enabled (false = already top). */
  canUp: boolean
  /** Whether the down arrow is enabled (false = already bottom). */
  canDown: boolean
  /** Optional rank number shown between the arrows. */
  rank?: number
  /** Paint the rank number in the accent (the rank-1 / preferred highlight). */
  topHighlighted?: boolean
  /** Disable BOTH arrows (e.g. a reorder is in flight). */
  disabled?: boolean
}>(), {
  rank: undefined,
  topHighlighted: false,
  disabled: false,
})

const emit = defineEmits<{
  /** A move was requested: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
}>()
</script>

<template>
  <div class="reorder">
    <button
      type="button"
      class="reorder__arrow"
      aria-label="Move up"
      :disabled="!canUp || disabled"
      @click="emit('move', -1)"
    >
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M18 15l-6-6-6 6" />
      </svg>
    </button>

    <span v-if="rank !== undefined" class="reorder__num" :class="{ 'reorder__num--top': topHighlighted }">{{ rank }}</span>

    <button
      type="button"
      class="reorder__arrow"
      aria-label="Move down"
      :disabled="!canDown || disabled"
      @click="emit('move', 1)"
    >
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M6 9l6 6 6-6" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.reorder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  flex: none;
}

.reorder__arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 18px;
  padding: 0;
  border-radius: var(--radius-xs);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  cursor: pointer;
  transition: color 0.15s, border-color 0.15s;
}

.reorder__arrow:hover:not(:disabled) {
  color: var(--text);
  border-color: var(--border2);
}

.reorder__arrow:disabled {
  color: var(--faint);
  opacity: 0.4;
  cursor: default;
}

.reorder__num {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 7px;
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.reorder__num--top {
  background: var(--accent);
  color: var(--cover-text);
}
</style>
