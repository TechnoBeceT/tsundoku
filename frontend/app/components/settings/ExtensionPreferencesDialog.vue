<script setup lang="ts">
import Dialog from '../ui/Dialog.vue'
import Spinner from '../ui/Spinner.vue'
import FormError from '../ui/FormError.vue'
import EmptyState from '../ui/EmptyState.vue'
import Toggle from '../ui/Toggle.vue'
import Tag from '../ui/Tag.vue'
import DisclosurePanel from '../ui/DisclosurePanel.vue'
import SourcePreferenceControl from './SourcePreferenceControl.vue'
import SourceSeriesPanel from './SourceSeriesPanel.vue'
import { preferenceKey, type MigrationBanner, type SourcePreferenceValue } from '~/composables/useSourcePreferences'
import { relativeTime, absoluteTime } from '~/utils/timeFormat'
import type { SourceSeriesRow } from './sourceSeries.types'
import type { components } from '~/utils/api/schema.d.ts'

type Group = components['schemas']['SourcePreferencesGroup']

/**
 * ExtensionPreferencesDialog — the "Configure" dialog for an installed extension.
 * Presentation-only: it renders the extension's per-source preferences (grouped
 * by language source) and emits a `change` for every committed edit; the parent
 * owns the composable that loads + writes. Each source's list is a fresh read,
 * and after a write the parent swaps in the refreshed list, so the dialog always
 * reflects the engine host's authoritative state (§16).
 *
 * Each group header carries a per-source PAUSE Switch — turning it off is a FULL
 * pause (QCAT-513): the source is skipped by the refresh sweep and the download/
 * upgrade dispatcher, so nothing fetches from it and another provider wins new
 * chapters. Downloaded chapters keep their files and stay readable — a pause
 * deletes nothing. A paused group shows a "Paused" tag + "Paused <when>" (from
 * `disabledSince`) and COLLAPSES its preference block (a paused source's settings
 * are irrelevant until it is resumed). Re-enabling resumes it on the next cycle.
 *
 * Each group also carries a per-source "Ignore scanlator" Toggle — flagging it
 * ON collapses that source's per-uploader providers into one [Source] provider
 * on FUTURE adopts (an uploader-in-scanlator source, e.g. Hive Scans). It is
 * apply-forward only (never migrates an already-adopted series) and always
 * visible (independent of the pause state).
 *
 * Each group finally carries a collapsible "Affected series" panel — the
 * read-only view of which adopted series carry the source, each flagged whether
 * it "goes dark" (pausing leaves it with no provider) or which provider takes
 * over. It loads LAZILY: the parent fetches only when a group's panel is revealed
 * (`toggle-impact`), and at most one is open at a time (`impactSourceId`).
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `extensionName`: the extension's display name (dialog title).
 *   - `groups`: the per-source preference groups.
 *   - `pending`: the preferences are still loading.
 *   - `error`: a load failure message (or null).
 *   - `savingKey`: `${sourceId}:${key}` of the preference being written (or null).
 *   - `saveError`: a write failure message (or null).
 *   - `enablingKey`: the sourceId whose enable/disable toggle is being written (or null).
 *   - `enableError`: an enable/disable write failure message (or null).
 *   - `ignoringKey`: the sourceId whose ignore-scanlator toggle is being written (or null).
 *   - `ignoreError`: an ignore-scanlator write failure message (or null).
 *
 * Emits `update:open` (v-model), `change` (a committed preference edit),
 * `toggle-enabled` (a committed enable/disable flip), and
 * `toggle-ignore-scanlator` (a committed ignore-scanlator flip).
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
  /** `${sourceId}:${key}` of the preference being written. */
  savingKey?: string | null
  /** A write failure message. */
  saveError?: string | null
  /** The sourceId whose enable/disable toggle is being written. */
  enablingKey?: string | null
  /** An enable/disable write failure message. */
  enableError?: string | null
  /** The sourceId whose ignore-scanlator toggle is being written. */
  ignoringKey?: string | null
  /** An ignore-scanlator write failure message. */
  ignoreError?: string | null
  /** The on-enable collapse migration banner (message + tone), or null when none ran. */
  migrationMessage?: MigrationBanner | null
  /** The sourceId whose "Affected series" panel is open (null = none). */
  impactSourceId?: string | null
  /** The open panel's affected series (only meaningful for `impactSourceId`). */
  impactRows?: SourceSeriesRow[]
  /** Whether the affected-series load is in flight. */
  impactPending?: boolean
  /** An affected-series load failure message (or null). */
  impactError?: string | null
}>(), {
  extensionName: '',
  groups: () => [],
  pending: false,
  error: null,
  savingKey: null,
  saveError: null,
  enablingKey: null,
  enableError: null,
  ignoringKey: null,
  ignoreError: null,
  migrationMessage: null,
  impactSourceId: null,
  impactRows: () => [],
  impactPending: false,
  impactError: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** A committed preference edit — forwarded from a control. */
  'change': [payload: { sourceId: string, key: string, value: SourcePreferenceValue }]
  /** A committed enable/disable flip — forwarded from a group's Switch. */
  'toggle-enabled': [payload: { sourceId: string, enabled: boolean }]
  /** A committed ignore-scanlator flip — forwarded from a group's Toggle. */
  'toggle-ignore-scanlator': [payload: { sourceId: string, ignoreScanlator: boolean }]
  /** A group's "Affected series" panel was opened/closed — carries the sourceId. */
  'toggle-impact': [sourceId: string]
}>()

// A control is busy when its (sourceId, key) matches the saving key.
function rowBusy(sourceId: string, key: string): boolean {
  return props.savingKey === preferenceKey(sourceId, key)
}

// A group's enable/disable Switch is busy while its own toggle write is in flight.
function enableBusy(sourceId: string): boolean {
  return props.enablingKey === sourceId
}

// A group's ignore-scanlator Toggle is busy while its own write is in flight.
function ignoreBusy(sourceId: string): boolean {
  return props.ignoringKey === sourceId
}

function toggleEnabled(sourceId: string, enabled: boolean): void {
  emit('toggle-enabled', { sourceId, enabled })
}

function toggleIgnoreScanlator(sourceId: string, ignoreScanlator: boolean): void {
  emit('toggle-ignore-scanlator', { sourceId, ignoreScanlator })
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
    <div v-if="ignoreError" class="prefs__saveerror">
      <FormError :message="ignoreError" />
    </div>
    <!-- The on-enable collapse migration result. A SUCCESS banner confirms
         already-adopted files were relabeled; a WARNING banner (tone) makes a
         total failure loud instead of silent (nothing relabeled → owner retries). -->
    <p
      v-if="migrationMessage"
      :class="['prefs__migration', `prefs__migration--${migrationMessage.tone}`]"
    >{{ migrationMessage.message }}</p>
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
          <span class="prefs__sourcename" :class="{ 'prefs__sourcename--off': !group.enabled }">{{ group.sourceName }}</span>
          <span class="prefs__lang">{{ group.lang.toUpperCase() }}</span>
          <!-- A paused source reads as PAUSED (not "failed"): the pill + a
               "Paused <when>" hint from disabledSince, precise time on hover. -->
          <Tag v-if="!group.enabled" tone="frost" size="sm">Paused</Tag>
          <span class="prefs__spacer" />
          <span
            v-if="!group.enabled && group.disabledSince"
            class="prefs__since"
            :title="absoluteTime(group.disabledSince)"
          >Paused {{ relativeTime(group.disabledSince) }}</span>
          <Spinner v-if="enableBusy(group.sourceId)" :size="15" tone="accent" />
          <!-- eslint-disable vue/attribute-hyphenation -->
          <!-- camelCase :ariaLabel is required: a bound kebab :aria-label routes to
               the native ARIA attribute, leaving Toggle's REQUIRED ariaLabel prop
               unset (a vue-tsc type error) — mirrors SourcePreferenceControl.
               Off = paused (skipped by refresh + downloads); on = active. -->
          <Toggle
            :model-value="group.enabled"
            :disabled="enableBusy(group.sourceId)"
            :ariaLabel="`Pause ${group.sourceName} (${group.lang})`"
            @update:model-value="toggleEnabled(group.sourceId, $event)"
          />
          <!-- eslint-enable vue/attribute-hyphenation -->
        </header>

        <!-- Per-source "Ignore scanlator" flag. Always visible (independent of
             the enable/disable state): it changes how FUTURE adopts interpret
             this source's chapters, collapsing per-uploader providers into one.
             Apply-forward only — it never migrates an already-adopted series. -->
        <div class="prefs__flagrow">
          <div class="prefs__flaglabel">
            <span class="prefs__flagname">Ignore scanlator</span>
            <span class="prefs__flaghint">Merge per-uploader chapters into one provider — for sources that put the uploader in the scanlator field (applies to new adopts).</span>
          </div>
          <Spinner v-if="ignoreBusy(group.sourceId)" :size="15" tone="accent" />
          <!-- eslint-disable vue/attribute-hyphenation -->
          <!-- camelCase :ariaLabel is required (see the enable Switch above). -->
          <Toggle
            :model-value="group.ignoreScanlator"
            :disabled="ignoreBusy(group.sourceId)"
            :ariaLabel="`Ignore scanlator for ${group.sourceName} (${group.lang})`"
            @update:model-value="toggleIgnoreScanlator(group.sourceId, $event)"
          />
          <!-- eslint-enable vue/attribute-hyphenation -->
        </div>

        <!-- A paused source's preferences are collapsed — they are irrelevant
             until the source is resumed. -->
        <template v-if="group.enabled">
          <p v-if="group.preferences.length === 0" class="prefs__none">No preferences for this source.</p>
          <SourcePreferenceControl
            v-for="pref in group.preferences"
            :key="`${group.sourceId}:${pref.key}`"
            :preference="pref"
            :source-id="group.sourceId"
            :busy="rowBusy(group.sourceId, pref.key)"
            @change="emit('change', $event)"
          />
        </template>
        <p v-else class="prefs__disabled">Paused — skipped by refresh and downloads. Downloaded chapters stay readable.</p>

        <!-- Read-only "what depends on this source" impact panel. Loads lazily:
             the parent fetches only when this group's panel is revealed, and at
             most one is open at a time (impactSourceId). -->
        <DisclosurePanel
          flat
          collapsible
          title="Affected series"
          :open="impactSourceId === group.sourceId"
          @update:open="emit('toggle-impact', group.sourceId)"
        >
          <SourceSeriesPanel
            v-if="impactSourceId === group.sourceId"
            :rows="impactRows"
            :pending="impactPending"
            :error="impactError"
          />
        </DisclosurePanel>
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

.prefs__sourcename--off {
  color: var(--muted);
}

.prefs__disabled {
  padding: 8px 2px;
  font-size: var(--text-sm);
  color: var(--faint);
  font-style: italic;
}

/* "Paused <when>" hint — quiet, right-aligned before the pause Switch. */
.prefs__since {
  font-size: var(--text-xs);
  color: var(--muted);
  white-space: nowrap;
}

/* Per-source ignore-scanlator flag row — a label + hint on the left, the Toggle
   on the right, sitting between the group header and its preference block. */
.prefs__flagrow {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
}

.prefs__flaglabel {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.prefs__flagname {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.prefs__flaghint {
  font-size: var(--text-xs);
  color: var(--muted);
}

/* On-enable collapse migration result banner. Success = positive confirmation
   that already-adopted files were merged + relabeled; warning = nothing was
   relabeled (a total failure) so it reads as a problem, not a success. */
.prefs__migration {
  font-size: var(--text-sm);
  border-radius: 8px;
  padding: 8px 12px;
  margin: 0 0 8px;
}

.prefs__migration--success {
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 10%, transparent);
}

.prefs__migration--warning {
  color: var(--danger-text);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
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
  /* Source name + lang pill on one unwrapping line can crowd the dialog's
   * already-narrow phone width. Let it wrap rather than crush (QCAT-230). */
  .prefs__grouphead {
    flex-wrap: wrap;
  }

  .prefs__sourcename {
    min-width: 0;
  }
}
</style>
