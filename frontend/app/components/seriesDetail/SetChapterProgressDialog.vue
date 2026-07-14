<script setup lang="ts">
import { computed } from 'vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import FormError from '../ui/FormError.vue'

/**
 * SetChapterProgressDialog — QCAT-242 entry point B: the confirm prompt for a
 * chapter row's "Set as current progress" action. A thin `ConfirmModal`
 * wrapper (mirrors `RemoveSourceDialog`) supplying the chapter-specific copy;
 * the actual target number is resolved by the caller (the page), which owns
 * the `useSeriesDetail.setReadingProgress` call and the pending target.
 *
 * Controlled via `v-model:open`; `busy` spins the confirm button + blocks
 * dismissal. Emits `confirm` when the owner confirms.
 *
 * §16: a FAILED reset keeps the dialog open with the reason shown inline
 * (`error`) — the owner never confirms into the void. The parent closes the
 * dialog only once the reset actually succeeded.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** The chapter number this row targets; null when it can't be resolved. */
  chapterNumber: number | null
  /** A failed-reset message to show inside the dialog, or null when there is none. */
  error?: string | null
}>(), {
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The reset was confirmed. */
  'confirm': []
}>()

// Never render a blank target (the row vanished / was mid-refresh when
// confirmed) — fall back to the generic heading rather than "chapter null".
const title = computed(() =>
  props.chapterNumber === null ? 'Set current progress?' : `Set progress to chapter ${props.chapterNumber}?`,
)
</script>

<template>
  <ConfirmModal
    :open="open"
    :busy="busy"
    :title="title"
    message="Later chapters become unread and every bound tracker jumps to this chapter."
    confirm-label="Set progress"
    @update:open="emit('update:open', $event)"
    @confirm="emit('confirm')"
  >
    <FormError v-if="error" class="set-progress__error" :message="error" />
  </ConfirmModal>
</template>

<style scoped>
.set-progress__error {
  margin-bottom: 4px;
}
</style>
