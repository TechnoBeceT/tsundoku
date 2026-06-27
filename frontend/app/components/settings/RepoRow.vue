<script setup lang="ts">
import IconButton from '../ui/IconButton.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import Spinner from '../ui/Spinner.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Repo } from '../screens/settings.types'

/**
 * RepoRow — one extension-repository URL row: the reorder arrows, the (truncated,
 * monospace) URL, an optional DEFAULT pill, and the remove button. A `busy` row
 * dims + shows an inline spinner while its mutation is in flight (§16).
 *
 *   - `repo`: the repository to render.
 *   - `canUp` / `canDown`: whether the reorder arrows are enabled.
 *   - `busy`: this row's mutation is in flight.
 *
 * Emits `move` (-1/1) and `remove`.
 */
defineProps<{
  /** The repository to render. */
  repo: Repo
  /** Whether the up arrow is enabled. */
  canUp: boolean
  /** Whether the down arrow is enabled. */
  canDown: boolean
  /** This row's mutation is in flight. */
  busy: boolean
}>()

const emit = defineEmits<{
  /** A reorder was requested: -1 = up, 1 = down. */
  'move': [direction: MoveDirection]
  /** Remove this repository. */
  'remove': []
}>()
</script>

<template>
  <div class="repo-row" :class="{ 'repo-row--busy': busy }">
    <ReorderControl :can-up="canUp" :can-down="canDown" :disabled="busy" @move="emit('move', $event)" />
    <span class="repo-row__url">{{ repo.url }}</span>
    <span v-if="repo.isDefault" class="pill">DEFAULT</span>
    <Spinner v-if="busy" :size="13" tone="current" />
    <IconButton variant="danger" aria-label="Remove" :disabled="busy" @click="emit('remove')">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /></svg>
    </IconButton>
  </div>
</template>

<style scoped>
.repo-row {
  display: flex;
  align-items: center;
  gap: 11px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 13px;
  margin-bottom: 9px;
}

/* In-flight row dims + blocks pointer input while its mutation runs (§16). */
.repo-row--busy {
  opacity: 0.6;
  pointer-events: none;
}

.repo-row__url {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pill {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
}
</style>
