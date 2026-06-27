<script setup lang="ts">
import ConfirmModal from '../ui/ConfirmModal.vue'

/**
 * RemoveSourceDialog — the confirm prompt for removing one source feed from a
 * series. A thin `ConfirmModal` (destructive) wrapper that supplies the
 * source-specific copy. Controlled via `v-model:open`; `busy` spins the confirm
 * button + blocks dismissal. Emits `confirm` when the owner confirms removal.
 */
defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** The source name, shown in the heading. */
  sourceName: string
}>()

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The removal was confirmed. */
  'confirm': []
}>()
</script>

<template>
  <ConfirmModal
    :open="open"
    :busy="busy"
    :title="`Remove “${sourceName}”?`"
    message="This removes the source feed only. All downloaded CBZ files and chapters are kept. You can re-add it later."
    confirm-label="Remove source"
    destructive
    @update:open="emit('update:open', $event)"
    @confirm="emit('confirm')"
  />
</template>
