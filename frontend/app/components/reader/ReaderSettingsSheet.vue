<script setup lang="ts">
import Dialog from '~/components/ui/Dialog.vue'
import Toggle from '~/components/ui/Toggle.vue'
import SegmentedToggle from '~/components/ui/SegmentedToggle.vue'
import type { SegmentOption } from '~/components/ui/controls.types'
import type { ReaderSettings } from '~/composables/useReaderSettings'

/**
 * ReaderSettingsSheet — the reader's global display-settings panel, presented in
 * the shared `Dialog` shell. It exposes the four v1 knobs and is PRESENTATION-
 * ONLY: it renders the current `settings` and emits each change; the composable
 * (`useReaderSettings`) owns persistence.
 *
 *   - Side padding (%)  — a range slider.
 *   - Page fit          — a SegmentedToggle: 'Max width' (cap) vs 'Full width'.
 *   - Max width (px)    — a range slider, shown only when fit === 'max'.
 *   - Page gaps         — a Toggle on/off switch.
 *
 * Props: `open` (v-model:open, via `update:open`) + the current `settings`.
 * Emits: `update:open` (Dialog open state) and `change` with a partial patch the
 * parent hands to `useReaderSettings.update`.
 */
defineProps<{
  /** Whether the sheet is shown (v-model:open). */
  open: boolean
  /** The current global reader settings to render. */
  settings: ReaderSettings
}>()

const emit = defineEmits<{
  /** The sheet open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** A single setting changed — a partial patch for the parent to persist. */
  'change': [patch: Partial<ReaderSettings>]
}>()

/** Page-fit options for the SegmentedToggle (keys match `ReaderSettings['fit']`). */
const fitOptions: SegmentOption[] = [
  { key: 'max', label: 'Max width' },
  { key: 'width', label: 'Full width' },
]

/** Reads a range input's numeric value from its change event. */
function rangeValue(event: Event): number {
  return Number((event.target as HTMLInputElement).value)
}
</script>

<template>
  <Dialog
    :open="open"
    title="Reader settings"
    @update:open="emit('update:open', $event)"
  >
    <div class="rs">
      <label class="rs__row">
        <span class="rs__label">
          Side padding
          <em class="rs__value">{{ settings.sidePaddingPct }}%</em>
        </span>
        <input
          class="rs__range"
          type="range"
          min="0"
          max="25"
          step="1"
          :value="settings.sidePaddingPct"
          @input="emit('change', { sidePaddingPct: rangeValue($event) })"
        >
      </label>

      <div class="rs__row rs__row--inline">
        <span class="rs__label">Page fit</span>
        <SegmentedToggle
          :model-value="settings.fit"
          :options="fitOptions"
          @update:model-value="emit('change', { fit: $event as ReaderSettings['fit'] })"
        />
      </div>

      <label v-if="settings.fit === 'max'" class="rs__row">
        <span class="rs__label">
          Max width
          <em class="rs__value">{{ settings.maxWidthPx }}px</em>
        </span>
        <input
          class="rs__range"
          type="range"
          min="480"
          max="1400"
          step="20"
          :value="settings.maxWidthPx"
          @input="emit('change', { maxWidthPx: rangeValue($event) })"
        >
      </label>

      <div class="rs__row rs__row--inline">
        <span class="rs__label">Page gaps</span>
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <Toggle :ariaLabel="'Page gaps'" :model-value="settings.gaps" @update:model-value="emit('change', { gaps: $event })" />
      </div>
    </div>
  </Dialog>
</template>

<style scoped>
.rs {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding-top: 4px;
}

.rs__row {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

/* The fit + gaps rows put the control on the right of the label. */
.rs__row--inline {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
}

.rs__label {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.rs__value {
  font-style: normal;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--accentBright);
}

.rs__range {
  width: 100%;
  height: 5px;
  appearance: none;
  -webkit-appearance: none;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  cursor: pointer;
}

.rs__range::-webkit-slider-thumb {
  appearance: none;
  -webkit-appearance: none;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--accent);
  border: 2px solid var(--cover-text);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
}

.rs__range::-moz-range-thumb {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--accent);
  border: 2px solid var(--cover-text);
}

.rs__range:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}
</style>
