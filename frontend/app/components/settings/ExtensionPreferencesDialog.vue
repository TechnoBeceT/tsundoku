<script setup lang="ts">
import Dialog from '../ui/Dialog.vue'
import Spinner from '../ui/Spinner.vue'
import FormError from '../ui/FormError.vue'
import EmptyState from '../ui/EmptyState.vue'
import Toggle from '../ui/Toggle.vue'
import SourcePreferenceControl from './SourcePreferenceControl.vue'
import { preferenceKey, type SourcePreferenceValue } from '~/composables/useSourcePreferences'
import type { components } from '~/utils/api/schema.d.ts'

type Group = components['schemas']['SourcePreferencesGroup']

/**
 * ExtensionPreferencesDialog — the "Configure" dialog for an installed extension.
 * Presentation-only: it renders the extension's per-source preferences (grouped
 * by language source) and emits a `change` for every committed edit; the parent
 * owns the composable that loads + writes. Each source's list is a fresh read,
 * and after a write the parent swaps in the refreshed list, so positions never go
 * stale (§16).
 *
 * Each group header also carries a per-language enable/disable Switch —
 * disabling hides that language from Discover/Search/Browse without touching
 * series already adopted from it (a series keeps updating regardless).
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `extensionName`: the extension's display name (dialog title).
 *   - `groups`: the per-source preference groups.
 *   - `pending`: the preferences are still loading.
 *   - `error`: a load failure message (or null).
 *   - `savingKey`: `${sourceId}:${position}` of the preference being written (or null).
 *   - `saveError`: a write failure message (or null).
 *   - `enablingKey`: the sourceId whose enable/disable toggle is being written (or null).
 *   - `enableError`: an enable/disable write failure message (or null).
 *
 * Emits `update:open` (v-model), `change` (a committed preference edit), and
 * `toggle-enabled` (a committed enable/disable flip).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The extension's display name (dialog title). */
  extensionName?: string
  /** The per-source preference groups. */
  groups?: Group[]
  /** The preferences are still loading. */
  pending?: boolean
  /** A load failure message. */
  error?: string | null
  /** `${sourceId}:${position}` of the preference being written. */
  savingKey?: string | null
  /** A write failure message. */
  saveError?: string | null
  /** The sourceId whose enable/disable toggle is being written. */
  enablingKey?: string | null
  /** An enable/disable write failure message. */
  enableError?: string | null
}>(), {
  extensionName: '',
  groups: () => [],
  pending: false,
  error: null,
  savingKey: null,
  saveError: null,
  enablingKey: null,
  enableError: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** A committed preference edit — forwarded from a control. */
  'change': [payload: { sourceId: string, position: number, value: SourcePreferenceValue }]
  /** A committed enable/disable flip — forwarded from a group's Switch. */
  'toggle-enabled': [payload: { sourceId: string, enabled: boolean }]
}>()

// A control is busy when its (sourceId, position) matches the saving key.
function rowBusy(sourceId: string, position: number): boolean {
  return props.savingKey === preferenceKey(sourceId, position)
}

// A group's enable/disable Switch is busy while its own toggle write is in flight.
function enableBusy(sourceId: string): boolean {
  return props.enablingKey === sourceId
}

function toggleEnabled(sourceId: string, enabled: boolean): void {
  emit('toggle-enabled', { sourceId, enabled })
}
</script>

<template>
  <Dialog
    :open="open"
    :title="extensionName ? `Configure ${extensionName}` : 'Configure extension'"
    @update:open="emit('update:open', $event)"
  >
    <!-- A write failure is surfaced for the whole dialog (§16). -->
    <div v-if="saveError" class="prefs__saveerror">
      <FormError :message="saveError" />
    </div>
    <div v-if="enableError" class="prefs__saveerror">
      <FormError :message="enableError" />
    </div>

    <div v-if="pending" class="prefs__loading">
      <Spinner :size="20" tone="accent" />
      <span>Loading preferences…</span>
    </div>

    <p v-else-if="error" class="prefs__error">{{ error }}</p>

    <EmptyState
      v-else-if="groups.length === 0"
      title="No configurable preferences"
      sub="This extension exposes no per-source settings."
    />

    <div v-else class="prefs">
      <section v-for="group in groups" :key="group.sourceId" class="prefs__group">
        <header class="prefs__grouphead">
          <span class="prefs__sourcename">{{ group.sourceName }}</span>
          <span class="prefs__lang">{{ group.lang.toUpperCase() }}</span>
          <span class="prefs__spacer" />
          <Spinner v-if="enableBusy(group.sourceId)" :size="15" tone="accent" />
          <!-- eslint-disable vue/attribute-hyphenation -->
          <!-- camelCase :ariaLabel is required: a bound kebab :aria-label routes to
               the native ARIA attribute, leaving Toggle's REQUIRED ariaLabel prop
               unset (a vue-tsc type error) — mirrors SourcePreferenceControl. -->
          <Toggle
            :model-value="group.enabled"
            :disabled="enableBusy(group.sourceId)"
            :ariaLabel="`Enable ${group.sourceName} (${group.lang})`"
            @update:model-value="toggleEnabled(group.sourceId, $event)"
          />
          <!-- eslint-enable vue/attribute-hyphenation -->
        </header>

        <p v-if="group.preferences.length === 0" class="prefs__none">No preferences for this source.</p>
        <SourcePreferenceControl
          v-for="pref in group.preferences"
          :key="`${group.sourceId}:${pref.position}`"
          :preference="pref"
          :source-id="group.sourceId"
          :busy="rowBusy(group.sourceId, pref.position)"
          @change="emit('change', $event)"
        />
      </section>
    </div>
  </Dialog>
</template>

<style scoped>
.prefs__saveerror {
  margin-bottom: 12px;
}

.prefs__loading {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 20px 2px;
  font-size: var(--text-sm);
  color: var(--muted);
}

.prefs__error {
  padding: 10px 2px;
  font-size: var(--text-sm);
  color: var(--danger-text);
}

.prefs__group {
  margin-bottom: 18px;
}

.prefs__grouphead {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.prefs__spacer {
  flex: 1;
}

.prefs__sourcename {
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--text);
}

.prefs__lang {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  padding: 1px 6px;
  border-radius: var(--radius-xs);
  background: var(--surface3);
  color: var(--muted);
}

.prefs__none {
  padding: 8px 2px;
  font-size: var(--text-sm);
  color: var(--faint);
}

@media (max-width: 900px) {
  /* Source name + lang pill + enable toggle on one unwrapping line can crowd out
   * the toggle inside the dialog's already-narrow phone width. Let it wrap
   * rather than crush (QCAT-230). */
  .prefs__grouphead {
    flex-wrap: wrap;
  }

  .prefs__sourcename {
    min-width: 0;
  }
}
</style>
