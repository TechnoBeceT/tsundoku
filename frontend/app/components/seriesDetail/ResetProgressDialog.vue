<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import FormError from '../ui/FormError.vue'
import SegmentedToggle from '../ui/SegmentedToggle.vue'
import TextField from '../ui/TextField.vue'
import type { SegmentOption } from '../ui/controls.types'

/**
 * ResetProgressDialog — QCAT-242 entry point A: the Trackers-section "Reset
 * progress" action. Lets the owner pick "Re-read from start" (target chapter
 * 0) or "Set to chapter N", then confirms a single series-wide reset that
 * marks local chapters read/unread around that target AND force-sets every
 * bound tracker to it.
 *
 * Controlled via `v-model:open`; the mode + chapter input reset every time it
 * opens (mirrors `DeleteSeriesDialog`), so a re-open never inherits a stale
 * pick. `busy` spins the confirm button and blocks dismissal (§16); `error`
 * (the mutation's real 4xx message) renders inline via `FormError` so a
 * failure keeps the dialog open with the reason visible, never silent.
 *
 * Emits `confirm` with the resolved target chapter number (0 for "from
 * start", else the typed chapter) — the caller owns the actual API call and
 * decides whether/when to close the dialog.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** A failed-reset message to show inline, or null when there is none. */
  error?: string | null
  /** Prefill for the "Set to chapter" field — the series' current furthest-read, or 1. */
  defaultChapter?: number
}>(), {
  busy: false,
  error: null,
  defaultChapter: 1,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The reset was confirmed — carries the resolved target chapter (0 = from start). */
  'confirm': [chapter: number]
}>()

const modeOptions: SegmentOption[] = [
  { key: 'start', label: 'Re-read from start' },
  { key: 'chapter', label: 'Set to chapter' },
]

const mode = ref<'start' | 'chapter'>('chapter')
const chapterInput = ref(String(props.defaultChapter))

// Reset the pick every time the dialog opens so a re-open never inherits a
// stale mode/number from a previous visit (mirrors DeleteSeriesDialog).
watch(() => props.open, (isOpen) => {
  if (isOpen) {
    mode.value = 'chapter'
    chapterInput.value = String(props.defaultChapter)
  }
})

// The chapter field only makes sense (and only renders) in "chapter" mode —
// "from start" is always exactly 0, no input needed.
const parsedChapter = computed(() => {
  const n = Number(chapterInput.value)
  return Number.isFinite(n) && n >= 0 ? Math.floor(n) : null
})

const targetChapter = computed(() => (mode.value === 'start' ? 0 : parsedChapter.value))

const confirmDisabled = computed(() => props.busy || targetChapter.value === null)

const warningLine = computed(() => {
  const target = targetChapter.value ?? 0
  return target === 0
    ? 'Every chapter is marked unread; all trackers are reset to the start.'
    : `Chapters after ${target} are marked unread; all trackers are set to chapter ${target}.`
})

function onConfirm(): void {
  if (targetChapter.value === null) return
  emit('confirm', targetChapter.value)
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="busy"
    title="Reset reading progress"
    @update:open="emit('update:open', $event)"
  >
    <SegmentedToggle
      class="reset__mode"
      :model-value="mode"
      :options="modeOptions"
      @update:model-value="mode = $event as 'start' | 'chapter'"
    />

    <TextField
      v-if="mode === 'chapter'"
      v-model="chapterInput"
      class="reset__field"
      label="Chapter"
      type="number"
    />

    <p class="reset__warning">{{ warningLine }}</p>

    <FormError v-if="error" class="reset__error" :message="error" />

    <template #actions>
      <AppButton variant="ghost" size="md" :disabled="busy" @click="emit('update:open', false)">
        Cancel
      </AppButton>
      <AppButton
        variant="primary"
        size="md"
        :loading="busy"
        :disabled="confirmDisabled"
        @click="onConfirm"
      >
        Apply
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.reset__mode {
  margin-bottom: 16px;
}

.reset__field {
  margin-bottom: 14px;
}

.reset__warning {
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
}

.reset__error {
  margin-top: 14px;
}
</style>
