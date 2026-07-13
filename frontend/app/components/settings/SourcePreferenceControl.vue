<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Toggle from '../ui/Toggle.vue'
import SelectField from '../ui/SelectField.vue'
import TextField from '../ui/TextField.vue'
import Spinner from '../ui/Spinner.vue'
import type { SelectOption } from '../ui/forms.types'
import type { components } from '~/utils/api/schema.d.ts'
import type { SourcePreferenceValue } from '~/composables/useSourcePreferences'

type Preference = components['schemas']['SourcePreference']

/**
 * SourcePreferenceControl — one row of the extension "Configure" dialog: a
 * source preference rendered as the control its `type` dictates:
 *   - CheckBoxPreference / SwitchPreference → a Toggle (commits on flip)
 *   - ListPreference                        → a SelectField (commits on select)
 *   - MultiSelectListPreference             → a checkbox group (commits on toggle)
 *   - EditTextPreference                    → a TextField (commits on Enter/blur)
 *
 * Toggles/selects commit immediately; the text field commits on Enter or blur
 * (never per-keystroke). Every commit emits `change` with the new value and the
 * preference's POSITION — the parent writes it and swaps in the refreshed list,
 * so positions never go stale (§16).
 *
 *   - `preference`: the preference to render.
 *   - `sourceId`: the owning source id (echoed in the change payload).
 *   - `busy`: this row's write is in flight (control disabled + spinner).
 */
const props = withDefaults(defineProps<{
  /** The preference to render. */
  preference: Preference
  /** The owning source id (echoed in the change payload). */
  sourceId: string
  /** This row's write is in flight. */
  busy?: boolean
}>(), {
  busy: false,
})

const emit = defineEmits<{
  /** A committed edit — carries the new value + the write coordinates. */
  'change': [payload: { sourceId: string, position: number, value: SourcePreferenceValue }]
}>()

function commit(value: SourcePreferenceValue): void {
  emit('change', { sourceId: props.sourceId, position: props.preference.position, value })
}

// The accessible label for the control (falls back to the key when untitled).
const controlLabel = computed(() => props.preference.title || props.preference.key)

const isBool = computed(() =>
  props.preference.type === 'CheckBoxPreference' || props.preference.type === 'SwitchPreference')
const isList = computed(() => props.preference.type === 'ListPreference')
const isMulti = computed(() => props.preference.type === 'MultiSelectListPreference')
const isText = computed(() => props.preference.type === 'EditTextPreference')

// ── Boolean (Toggle) ──────────────────────────────────────────────────────────
const boolValue = computed(() => props.preference.currentValue === true)

// ── List (SelectField) ────────────────────────────────────────────────────────
// entries are the labels, entryValues the stored values (matched by index).
const listOptions = computed<SelectOption[]>(() =>
  props.preference.entryValues.map((value, i) => ({
    value,
    label: props.preference.entries[i] ?? value,
  })))
const listValue = computed(() =>
  typeof props.preference.currentValue === 'string' ? props.preference.currentValue : '')

// ── MultiSelect (checkbox group) ──────────────────────────────────────────────
const selected = computed<string[]>(() =>
  Array.isArray(props.preference.currentValue) ? props.preference.currentValue : [])
function isChecked(value: string): boolean {
  return selected.value.includes(value)
}
function toggleMulti(value: string, checked: boolean): void {
  const next = checked
    ? [...selected.value, value]
    : selected.value.filter(v => v !== value)
  commit(next)
}

// ── EditText (TextField, buffered) ────────────────────────────────────────────
// A local buffer so the field commits on Enter/blur, not on every keystroke.
const textBuffer = ref('')
watch(() => props.preference.currentValue, (v) => {
  textBuffer.value = typeof v === 'string' ? v : ''
}, { immediate: true })
function commitText(): void {
  if (textBuffer.value !== (props.preference.currentValue ?? '')) commit(textBuffer.value)
}
</script>

<template>
  <div class="pref">
    <div class="pref__head">
      <div class="pref__meta">
        <span class="pref__title">{{ preference.title || preference.key }}</span>
        <span v-if="preference.summary" class="pref__summary">{{ preference.summary }}</span>
      </div>
      <div class="pref__control">
        <Spinner v-if="busy" :size="15" tone="accent" />
        <!-- eslint-disable vue/attribute-hyphenation -->
        <!-- camelCase :ariaLabel is required: a bound kebab :aria-label routes to
             the native ARIA attribute, leaving Toggle's REQUIRED ariaLabel prop
             unset (a vue-tsc type error). -->
        <Toggle
          v-if="isBool"
          :model-value="boolValue"
          :disabled="busy"
          :ariaLabel="controlLabel"
          @update:model-value="commit($event)"
        />
        <SelectField
          v-else-if="isList"
          :model-value="listValue"
          :options="listOptions"
          :disabled="busy"
          :ariaLabel="controlLabel"
          @update:model-value="commit($event)"
        />
        <!-- eslint-enable vue/attribute-hyphenation -->
      </div>
    </div>

    <!-- MultiSelect renders a full-width checkbox group below the title. -->
    <div v-if="isMulti" class="pref__multi">
      <label v-for="(value, i) in preference.entryValues" :key="value" class="pref__check">
        <input
          type="checkbox"
          :checked="isChecked(value)"
          :disabled="busy"
          @change="toggleMulti(value, ($event.target as HTMLInputElement).checked)"
        >
        <span>{{ preference.entries[i] ?? value }}</span>
      </label>
    </div>

    <!-- EditText renders a full-width buffered input committing on Enter/blur. -->
    <div v-if="isText" class="pref__text">
      <TextField
        :model-value="textBuffer"
        :disabled="busy"
        mono
        @update:model-value="textBuffer = $event"
        @enter="commitText"
        @blur="commitText"
      />
    </div>
  </div>
</template>

<style scoped>
.pref {
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
}

.pref__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.pref__meta {
  min-width: 0;
}

.pref__title {
  display: block;
  font-weight: var(--weight-semibold);
  font-size: var(--text-sm);
  color: var(--text);
}

.pref__summary {
  display: block;
  margin-top: 2px;
  font-size: var(--text-xs);
  color: var(--muted);
}

.pref__control {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  min-width: 120px;
  justify-content: flex-end;
}

.pref__multi {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 6px 14px;
  margin-top: 10px;
}

.pref__check {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: var(--text-sm);
  color: var(--muted);
  cursor: pointer;
}

.pref__check input {
  accent-color: var(--accent);
}

.pref__text {
  margin-top: 10px;
}

@media (max-width: 900px) {
  /* A long preference title beside a fixed min-width:120px control (Toggle or
   * SelectField) has no room to breathe on a phone. Wrap the control onto its
   * own full-width line under the title instead of crushing it (QCAT-230). */
  .pref__head {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .pref__control {
    min-width: 0;
    width: 100%;
    justify-content: flex-start;
  }
}
</style>
