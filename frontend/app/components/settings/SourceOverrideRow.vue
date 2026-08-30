<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import SettingRow from './SettingRow.vue'
import AppButton from '../ui/AppButton.vue'
import FormError from '../ui/FormError.vue'
import SelectField from '../ui/SelectField.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { SelectOption } from '../ui/forms.types'
import type { SourceConfigurationRowKey } from '../screens/settings.types'

export type { SourceConfigurationRowKey } from '../screens/settings.types'

type SourceOverrideControl = 'number' | 'text' | 'select' | 'toggle'
type SourceConfigurationRowValue = string | number | boolean

/**
 * SourceOverrideRow — one ordinary source exception. The left rail is the
 * status grammar: quiet for an inherited value, violet for an explicit source
 * override. The parent owns confirmed state and persistence; this component
 * buffers the editor and emits one keyed mutation at a time.
 */
const props = withDefaults(defineProps<{
  settingKey: SourceConfigurationRowKey
  name: string
  hint?: string
  control: SourceOverrideControl
  modelValue: SourceConfigurationRowValue
  globalValue: SourceConfigurationRowValue
  inherited: boolean
  options?: SelectOption[]
  saving?: boolean
  error?: string | null
}>(), {
  hint: '',
  options: () => [],
  saving: false,
  error: null,
})

const emit = defineEmits<{
  'set-override': [key: SourceConfigurationRowKey, value: string | number | boolean]
  'use-global': [key: SourceConfigurationRowKey]
}>()

// Only confirmed parent state re-seeds the editor. An error prop alone leaves
// the attempted edit available for correction while the confirmed rail stays
// unchanged.
const draft = shallowRef<SourceConfigurationRowValue>(props.modelValue)
watch(() => props.modelValue, value => {
  draft.value = value
})

const textDraft = computed({
  get: () => String(draft.value),
  set: (value: string) => {
    draft.value = value
  },
})

const selectDraft = computed(() => String(draft.value))
const toggleDraft = computed(() => Boolean(draft.value))

function displayValue(value: SourceConfigurationRowValue): string {
  if (typeof value === 'boolean') return value ? 'On' : 'Off'
  return String(value)
}

function updateSelect(value: string): void {
  draft.value = value
}

function updateToggle(value: boolean): void {
  draft.value = value
}

function setOverride(): void {
  if (props.control === 'number') {
    const value = Number(draft.value)
    if (Number.isFinite(value)) emit('set-override', props.settingKey, value)
    return
  }
  emit('set-override', props.settingKey, draft.value)
}
</script>

<template>
  <div
    class="source-override"
    :class="{ 'source-override--active': !inherited }"
    :aria-busy="saving"
  >
    <SettingRow :name="name" :hint="hint">
      <div class="source-override__content">
        <div class="source-override__status" aria-live="polite">
          <span class="source-override__mode">{{ inherited ? 'Inherited' : 'Override' }}</span>
          <span class="source-override__value">
            Global <span>{{ displayValue(globalValue) }}</span>
          </span>
          <span class="source-override__value">
            Effective <span data-testid="confirmed-value">{{ displayValue(modelValue) }}</span>
          </span>
        </div>

        <div class="source-override__editor">
          <TextField
            v-if="control === 'number' || control === 'text'"
            v-model="textDraft"
            :label="`${name} override`"
            :type="control === 'number' ? 'number' : 'text'"
            :mono="control === 'text'"
            :compact="control === 'number'"
            :disabled="saving"
            @enter="setOverride"
          />
          <!-- eslint-disable vue/attribute-hyphenation -->
          <SelectField
            v-else-if="control === 'select'"
            :model-value="selectDraft"
            :options="options"
            :disabled="saving"
            :ariaLabel="`${name} override`"
            @update:model-value="updateSelect"
          />
          <Toggle
            v-else
            :model-value="toggleDraft"
            :disabled="saving"
            :ariaLabel="`${name} override`"
            @update:model-value="updateToggle"
          />
          <!-- eslint-enable vue/attribute-hyphenation -->

          <div class="source-override__actions">
            <AppButton
              data-testid="set-override"
              variant="mini"
              size="xs"
              :loading="saving"
              @click="setOverride"
            >
              Set override
            </AppButton>
            <AppButton
              data-testid="use-global"
              variant="text"
              size="xs"
              :disabled="saving || inherited"
              @click="emit('use-global', settingKey)"
            >
              Use global
            </AppButton>
          </div>
        </div>
      </div>
    </SettingRow>
    <FormError v-if="error" class="source-override__error" :message="error" />
  </div>
</template>

<style scoped>
.source-override {
  min-width: 0;
  max-width: 100%;
  padding-left: var(--space-md);
  border-left: 3px solid var(--border2);
}

.source-override--active {
  border-left-color: var(--accent);
}

.source-override__content {
  display: flex;
  align-items: flex-end;
  justify-content: flex-end;
  gap: var(--space-base);
  min-width: 0;
  max-width: 100%;
}

.source-override__status {
  display: grid;
  gap: var(--space-3xs);
  min-width: 0;
  max-width: 17rem;
  color: var(--faint);
  font-size: var(--text-xs);
}

.source-override__mode {
  color: var(--muted);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.source-override--active .source-override__mode {
  color: var(--accentBright);
}

.source-override__value {
  min-width: 0;
  overflow-wrap: anywhere;
}

.source-override__value span {
  color: var(--text);
  font-family: var(--font-mono);
}

.source-override__editor {
  display: flex;
  align-items: flex-end;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-xs);
  min-width: 0;
  max-width: 23rem;
}

.source-override__editor :deep(.field:not(.field--compact)),
.source-override__editor :deep(.select) {
  width: min(100%, 14rem);
}

.source-override__editor :deep(.select__el) {
  min-width: 0;
  text-overflow: ellipsis;
}

.source-override__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-xs-tight);
}

.source-override__error {
  padding: 0 0 var(--space-sm);
}

@media (max-width: 900px) {
  .source-override__content {
    align-items: flex-start;
    flex-direction: column;
    width: 100%;
  }

  .source-override__status,
  .source-override__editor {
    width: 100%;
    max-width: 100%;
    justify-content: flex-start;
  }
}
</style>
