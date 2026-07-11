<script setup lang="ts">
import { computed } from 'vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'

/**
 * RemoveSourceDialog — the confirm prompt for removing one source feed from a
 * series. A thin `ConfirmModal` (destructive) wrapper that supplies the
 * source-specific copy. Controlled via `v-model:open`; `busy` spins the confirm
 * button + blocks dismissal. Emits `confirm` when the owner confirms removal.
 *
 * §16: a FAILED removal keeps the dialog open and shows the reason inside it
 * (`error`) — the owner never confirms into the void. The parent closes the
 * dialog only once the removal actually succeeded.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** The source name, shown in the heading. May be "" when it can't be resolved. */
  sourceName: string
  /** A failed-removal message to show inside the dialog, or null when there is none. */
  error?: string | null
}>(), {
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The removal was confirmed. */
  'confirm': []
}>()

// Never render an empty quoted name (the `Remove “”?` degradation): when the
// target source can't be resolved — e.g. it vanished from the list mid-confirm —
// fall back to the generic heading.
const title = computed(() =>
  props.sourceName.trim() ? `Remove “${props.sourceName}”?` : 'Remove this source?',
)
</script>

<template>
  <ConfirmModal
    :open="open"
    :busy="busy"
    :title="title"
    message="This removes the source feed only. All downloaded CBZ files and chapters are kept. You can re-add it later."
    confirm-label="Remove source"
    destructive
    @update:open="emit('update:open', $event)"
    @confirm="emit('confirm')"
  >
    <ErrorBanner v-if="error" class="remove__error" :message="error" :dismissible="false" />
  </ConfirmModal>
</template>

<style scoped>
.remove__error {
  margin-bottom: 4px;
}
</style>
