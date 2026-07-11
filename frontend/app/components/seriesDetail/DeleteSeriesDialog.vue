<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import RadioCard from '../ui/RadioCard.vue'
import type { DeleteChoice } from '../screens/seriesDetail.types'

/**
 * DeleteSeriesDialog — the required-choice "delete series" dialog: the owner MUST
 * pick keep-files or also-wipe-files before the confirm button enables (it starts
 * unselected and disabled). The wipe choice carries the danger treatment.
 *
 * Controlled via `v-model:open`; resets the choice every time it opens, so a
 * re-open never inherits a stale selection. `busy` spins the confirm button and
 * blocks dismissal (§16). Emits `confirm` with the resolved `deleteFiles` boolean.
 *
 * §16: a delete that FAILS leaves the dialog open (a successful one navigates
 * away) — `error` shows the reason inside the dialog, where the owner is
 * looking, instead of only on the screen behind the overlay.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** The series title, shown in the heading. */
  seriesTitle: string
  /** A failed-delete message to show inside the dialog, or null when there is none. */
  error?: string | null
}>(), {
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The delete was confirmed — carries the required deleteFiles choice. */
  'confirm': [deleteFiles: boolean]
}>()

// Starts null: the owner MUST explicitly pick keep-or-wipe — the confirm button
// stays disabled until then. Reset on every open so it never inherits a choice.
const choice = ref<DeleteChoice | null>(null)
watch(() => props.open, (isOpen) => {
  if (isOpen) choice.value = null
})

// Confirm label reflects the chosen outcome (or the neutral default before a pick).
const confirmLabel = computed(() => {
  if (choice.value === 'wipe') return 'Delete + files'
  if (choice.value === 'keep') return 'Un-manage'
  return 'Delete'
})

function onConfirm() {
  if (choice.value) emit('confirm', choice.value === 'wipe')
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="busy"
    :title="`Delete “${seriesTitle}”?`"
    @update:open="emit('update:open', $event)"
  >
    <p class="delete__desc">Choose what happens to downloaded files. You must pick one.</p>

    <div class="delete__choices">
      <RadioCard :selected="choice === 'keep'" @select="choice = 'keep'">
        Keep files on disk
        <template #hint>Removes library tracking only. Recoverable later via a library rescan.</template>
      </RadioCard>

      <RadioCard variant="danger" :selected="choice === 'wipe'" @select="choice = 'wipe'">
        Also delete downloaded files
        <template #hint>Permanently removes all CBZ files from disk. This cannot be undone.</template>
      </RadioCard>
    </div>

    <ErrorBanner v-if="error" class="delete__error" :message="error" :dismissible="false" />

    <template #actions>
      <AppButton variant="ghost" size="md" :disabled="busy" @click="emit('update:open', false)">
        Cancel
      </AppButton>
      <AppButton
        :variant="choice === 'wipe' ? 'danger-ghost' : 'primary'"
        size="md"
        :loading="busy"
        :disabled="choice === null"
        @click="onConfirm"
      >
        {{ confirmLabel }}
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.delete__desc {
  margin: 0 0 18px;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}

.delete__choices {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.delete__error {
  margin-top: 14px;
}
</style>
