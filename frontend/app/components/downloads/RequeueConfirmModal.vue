<script setup lang="ts">
import { computed } from 'vue'
import ConfirmModal from '../ui/ConfirmModal.vue'

/**
 * RequeueConfirmModal — the bulk retry/reset confirmation for the Downloads
 * Failed tab. A thin `ConfirmModal` wrapper that phrases the prompt from the
 * `count` of chapters about to be requeued and reassures that files are never
 * deleted (requeue RESETS state, it doesn't delete CBZs).
 *
 *   - `open` (v-model:open): whether the modal is shown.
 *   - `count`: how many chapters the confirmed action will requeue.
 *
 * Emits `confirm`, `cancel`, and `update:open` (v-model) — the parent runs the
 * bulk retry on `confirm` and clears its pending scope on dismissal.
 */
const props = defineProps<{
  /** Whether the modal is open (v-model:open). */
  open: boolean
  /** Chapter count for the confirmation copy. */
  count: number
}>()

const emit = defineEmits<{
  /** The owner confirmed the requeue. */
  'confirm': []
  /** The owner cancelled / dismissed. */
  'cancel': []
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
}>()

const message = computed(() =>
  `This will requeue ${props.count} chapter${props.count > 1 ? 's' : ''}. They'll download on the next cycle. Files are never deleted.`,
)
</script>

<template>
  <ConfirmModal
    :open="open"
    title="Requeue chapters?"
    :message="message"
    confirm-label="Requeue"
    @confirm="emit('confirm')"
    @cancel="emit('cancel')"
    @update:open="emit('update:open', $event)"
  />
</template>
