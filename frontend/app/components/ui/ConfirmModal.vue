<script setup lang="ts">
import Dialog from './Dialog.vue'
import AppButton from './AppButton.vue'

/**
 * ConfirmModal — a thin `<Dialog>` wrapper for the confirm/cancel pattern (the
 * Series-Detail remove-source prompt, the Downloads requeue prompt, the Settings
 * rename/delete/uninstall prompts). Renders an optional message plus a
 * cancel + confirm button row; the default slot is extra body content (e.g. a
 * select or radio cards shown above the buttons).
 *
 *   - `open` (v-model:open): whether the modal is shown.
 *   - `title`: the heading (required — it's the accessible name).
 *   - `message`: an optional explanatory line under the title.
 *   - `confirmLabel` (default 'Confirm') / `cancelLabel` (default 'Cancel').
 *   - `destructive`: render the confirm button in the danger treatment.
 *   - `busy`: in-flight flag — spins the confirm button + blocks dismissal.
 *
 * Emits `confirm`, `cancel`, and `update:open` (v-model).
 */
withDefaults(defineProps<{
  /** Whether the modal is open (v-model:open). */
  open: boolean
  /** The heading + accessible name. */
  title: string
  /** Optional explanatory line. */
  message?: string
  /** Confirm button label. */
  confirmLabel?: string
  /** Cancel button label. */
  cancelLabel?: string
  /** Render the confirm button as a destructive action. */
  destructive?: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
}>(), {
  confirmLabel: 'Confirm',
  cancelLabel: 'Cancel',
  destructive: false,
  busy: false,
})

const emit = defineEmits<{
  /** The confirm button was pressed. */
  'confirm': []
  /** The cancel button (or any dismissal) was chosen. */
  'cancel': []
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
}>()

// Cancel closes the modal AND reports the intent so the parent can reset state.
function onCancel() {
  emit('cancel')
  emit('update:open', false)
}
</script>

<template>
  <Dialog
    :open="open"
    :title="title"
    :busy="busy"
    @update:open="emit('update:open', $event)"
    @close="emit('cancel')"
  >
    <p v-if="message" class="confirm__msg">{{ message }}</p>
    <slot />

    <template #actions>
      <AppButton variant="ghost" size="md" :disabled="busy" @click="onCancel">
        {{ cancelLabel }}
      </AppButton>
      <AppButton
        :variant="destructive ? 'danger-ghost' : 'primary'"
        size="md"
        :loading="busy"
        @click="emit('confirm')"
      >
        {{ confirmLabel }}
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.confirm__msg {
  margin: 0 0 18px;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}
</style>
