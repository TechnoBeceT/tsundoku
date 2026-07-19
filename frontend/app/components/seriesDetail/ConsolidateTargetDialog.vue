<script setup lang="ts">
import { ref, watch } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'

/**
 * ConsolidateTargetDialog — the target picker for the Series-Detail multi-provider
 * consolidation (QCAT-295 Part B). Given the SELECTED providers to fold away, the
 * owner picks the ONE survivor they fold into:
 *   - an EXISTING provider on the series (one of `candidates`, the non-selected
 *     rows) — confirms straight to the consolidate endpoint; OR
 *   - "Match to a new source…" — hands off to the existing MatchDiskProviderDialog
 *     on the page to search + pick a real source + scanlator (so the source-picker
 *     is reused, not re-implemented).
 *
 * Presentation-only: the selection is a local radio-group; on confirm it emits
 * either `confirm` (with the chosen existing provider id) or `matchToSource` (to
 * open the source picker). Controlled via `v-model:open`; `busy` spins confirm +
 * blocks dismissal; a failed consolidation surfaces via `error` INSIDE the dialog
 * (§16). Resets its selection every time it opens.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** How many sources are being folded (drives the heading copy). */
  selectedCount: number
  /** The candidate survivor providers (the non-selected rows) — {id, name}. */
  candidates: { id: string, name: string }[]
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** A failed-consolidation message to show inside the dialog, or null. */
  error?: string | null
}>(), {
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** An EXISTING provider was chosen as the survivor — carries its id. */
  'confirm': [existingProviderId: string]
  /** "Match to a new source…" was chosen — the page opens the source picker. */
  'matchToSource': []
}>()

// The MATCH_SENTINEL radio value stands for "Match to a new source…" (distinct
// from any provider UUID).
const MATCH_SENTINEL = '__match__'
const choice = ref<string>('')

// Reset the pick every time the dialog opens (mirrors the other dialogs'
// reset-on-open) so a re-open never inherits a stale selection.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) choice.value = ''
  },
)

const onConfirm = (): void => {
  if (!choice.value) return
  if (choice.value === MATCH_SENTINEL) {
    emit('matchToSource')
    return
  }
  emit('confirm', choice.value)
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="busy"
    title="Merge sources into…"
    @update:open="emit('update:open', $event)"
  >
    <p class="consolidate__lead">
      Fold the {{ selectedCount }} selected source{{ selectedCount === 1 ? '' : 's' }} into one survivor.
      The downloaded files are relabeled, never re-downloaded.
    </p>

    <ErrorBanner v-if="error" class="consolidate__error" :message="error" :dismissible="false" />

    <fieldset class="consolidate__options" :disabled="busy">
      <legend class="consolidate__legend">Choose the survivor</legend>

      <label v-for="c in candidates" :key="c.id" class="consolidate__option">
        <input v-model="choice" type="radio" :value="c.id" name="consolidate-target">
        <span class="consolidate__option-label">{{ c.name }}</span>
      </label>

      <label class="consolidate__option consolidate__option--match">
        <input v-model="choice" type="radio" :value="MATCH_SENTINEL" name="consolidate-target">
        <span class="consolidate__option-label">Match to a new source…</span>
      </label>

      <p v-if="candidates.length === 0" class="consolidate__note">
        No other source on this series — choose "Match to a new source" to attach one.
      </p>
    </fieldset>

    <template #actions>
      <AppButton variant="ghost" :disabled="busy" @click="emit('update:open', false)">Cancel</AppButton>
      <AppButton variant="primary" :disabled="busy || !choice" @click="onConfirm">
        {{ busy ? 'Merging…' : 'Merge' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.consolidate__lead {
  margin: 0 0 var(--space-md);
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
}

.consolidate__error {
  margin-bottom: var(--space-sm);
}

.consolidate__options {
  display: flex;
  flex-direction: column;
  gap: var(--space-2xs);
  margin: 0;
  padding: 0;
  border: 0;
}

.consolidate__legend {
  padding: 0;
  margin-bottom: var(--space-xs);
  font-size: 0.65625rem;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--faint);
}

.consolidate__option {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  padding: var(--space-sm) 0.6875rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--surface2);
  cursor: pointer;
}

.consolidate__option input {
  accent-color: var(--accent);
}

.consolidate__option--match {
  border-style: dashed;
}

.consolidate__option-label {
  font-size: var(--text-sm);
  color: var(--text);
}

.consolidate__note {
  margin: var(--space-2xs) 0 0;
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
