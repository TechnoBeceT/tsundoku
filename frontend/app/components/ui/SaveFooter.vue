<script setup lang="ts">
import AppButton from './AppButton.vue'
import type { SaveState } from './forms.types'

/**
 * SaveFooter — the §16 save row: a save button plus an inline result marker
 * (a success check or a visible error message). Used at the foot of the Settings
 * library + Suwayomi panes. The button spins + disables while saving and is also
 * disabled when there's nothing to save (`dirty` false).
 *
 *   - `state`: `{ status: 'idle' | 'saving' | 'success' | 'error'; error? }` —
 *     drives the spinner, the success check, and the surfaced error text.
 *   - `dirty` (default true): whether there are unsaved changes (gates the button).
 *   - `label` (default 'Save changes'): the button label.
 *
 * Emits `save` when the button is pressed.
 */
const props = withDefaults(defineProps<{
  /** The async save outcome (loading / success / error). */
  state: SaveState
  /** Whether there are unsaved changes — disables the button when false. */
  dirty?: boolean
  /** The save button label. */
  label?: string
}>(), {
  dirty: true,
  label: 'Save changes',
})

const emit = defineEmits<{
  /** The save button was pressed. */
  save: []
}>()

const isSaving = () => props.state.status === 'saving'
</script>

<template>
  <div class="save-foot">
    <span v-if="state.status === 'success'" class="save-result save-result--ok">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M20 6 9 17l-5-5" />
      </svg>
      Saved
    </span>
    <span v-else-if="state.status === 'error'" class="save-result save-result--err" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="10" />
        <path d="M12 8v4M12 16h.01" />
      </svg>
      {{ state.error || 'Save failed' }}
    </span>

    <AppButton
      variant="primary"
      size="md"
      type="submit"
      :loading="isSaving()"
      :disabled="!dirty"
      @click="emit('save')"
    >
      {{ label }}
    </AppButton>
  </div>
</template>

<style scoped>
.save-foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 14px;
}

.save-result {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
}

.save-result--ok {
  color: var(--set-ok-text);
}

.save-result--err {
  color: var(--danger-text);
}
</style>
