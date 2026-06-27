<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import IconButton from '../ui/IconButton.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import Spinner from '../ui/Spinner.vue'
import TextField from '../ui/TextField.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { SettingsCategory } from '../screens/settings.types'

/**
 * CategoryRow — one row in the Settings category CRUD list. Shows the category
 * name Chip, its series count, an optional DEFAULT pill, and the reorder/rename/
 * delete actions; when `renaming` it swaps the display for an inline rename field.
 * A `busy` row dims + blocks input and shows a "Working…" marker (§16).
 *
 * Presentation-only: rename text is v-modelled back up via `renameValue` and the
 * row emits every action for the parent pane to validate + dispatch.
 *
 *   - `category`: the category to render.
 *   - `canUp` / `canDown`: whether the reorder arrows are enabled.
 *   - `busy`: this row's mutation is in flight (dim + disable).
 *   - `renaming`: show the inline rename field instead of the display row.
 *   - `renameValue` (v-model:renameValue): the inline rename input's value.
 */
defineProps<{
  /** The category to render. */
  category: SettingsCategory
  /** Whether the up arrow is enabled. */
  canUp: boolean
  /** Whether the down arrow is enabled. */
  canDown: boolean
  /** This row's mutation is in flight. */
  busy: boolean
  /** Show the inline rename field. */
  renaming: boolean
  /** The inline rename input's value (v-model:renameValue). */
  renameValue: string
}>()

const emit = defineEmits<{
  /** A reorder was requested: -1 = up, 1 = down. */
  'move': [direction: MoveDirection]
  /** The inline rename field changed (v-model:renameValue). */
  'update:renameValue': [value: string]
  /** Enter the inline rename mode. */
  'start-rename': []
  /** Commit the inline rename. */
  'save-rename': []
  /** Abandon the inline rename. */
  'cancel-rename': []
  /** Mark this category as the default landing. */
  'set-default': []
  /** Begin deleting this category. */
  'start-delete': []
}>()
</script>

<template>
  <div class="cat-row" :class="{ 'cat-row--busy': busy }">
    <ReorderControl :can-up="canUp" :can-down="canDown" :disabled="busy" @move="emit('move', $event)" />

    <!-- Inline rename -->
    <div v-if="renaming" class="cat-edit" @keydown.esc="emit('cancel-rename')">
      <TextField
        class="cat-edit__field"
        :model-value="renameValue"
        @update:model-value="emit('update:renameValue', $event)"
        @enter="emit('save-rename')"
      />
      <AppButton variant="solid" size="sm" @click="emit('save-rename')">Save</AppButton>
      <AppButton variant="mini" size="sm" @click="emit('cancel-rename')">Cancel</AppButton>
    </div>

    <!-- Display -->
    <div v-else class="cat-main">
      <Chip variant="accent">{{ category.name }}</Chip>
      <span class="cat-count">{{ category.count }} series</span>
      <span v-if="category.isDefault" class="pill">DEFAULT</span>
      <span v-if="busy" class="row-busy"><Spinner :size="13" tone="current" />Working…</span>
      <div class="cat-actions">
        <AppButton v-if="!category.protected && !category.isDefault" variant="text" size="sm" :disabled="busy" @click="emit('set-default')">Set default</AppButton>
        <IconButton v-if="!category.protected" aria-label="Rename" :disabled="busy" @click="emit('start-rename')">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>
        </IconButton>
        <IconButton v-if="!category.protected" variant="danger" aria-label="Delete" :disabled="busy" @click="emit('start-delete')">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /></svg>
        </IconButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cat-row {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  margin-bottom: 9px;
  background: var(--surface2);
}

/* In-flight row dims + blocks pointer input while its mutation runs (§16). */
.cat-row--busy {
  opacity: 0.6;
  pointer-events: none;
}

.cat-main {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.cat-edit {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 8px;
}

.cat-edit__field {
  flex: 1;
}

.cat-count {
  font-size: 11.5px;
  color: var(--faint);
}

.cat-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 6px;
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

/* The small "Working…" marker shown beside a busy row. */
.row-busy {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--muted);
}
</style>
