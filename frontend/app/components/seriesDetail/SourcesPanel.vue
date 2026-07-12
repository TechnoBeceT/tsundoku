<script setup lang="ts">
import { computed } from 'vue'
import PanelCard from './PanelCard.vue'
import AppButton from '../ui/AppButton.vue'
import ProviderRow from './ProviderRow.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * SourcesPanel — the Series-Detail "Sources" card: a titled header with the
 * source-count pill and an Add button, then the importance-ranked `ProviderRow`
 * list (preferred first) or an empty note when nothing is tracked.
 * Presentation-only — the (already-sorted) providers arrive via props; the panel
 * re-emits each row's `move`/`remove`/`match` (keyed by source id) plus
 * `addSource`. Wraps the shared PanelCard shell: the count pill rides the
 * header-left `lead` slot (grouped with the title), the Add button the
 * header-right `actions` slot, and the provider list the full-bleed body.
 * PanelCard itself owns the scroll (`.panel__content`) — with 4-7 source
 * cards this panel used to grow unbounded and drag the whole page down with
 * it; it now scrolls internally exactly like ChaptersPanel does.
 *
 * Each row's chapter coverage comes from the provider itself (`feedCount` /
 * `feedRanges` on the series-detail response) — this panel fetches nothing and
 * triggers no source call.
 *
 * `driftedIds` (SeriesProvider ids the screen has flagged as drifted
 * duplicates, e.g. via `findDriftedProviderIds`) drives a danger banner +
 * "Clean up" button at the top of the body plus a DUPLICATE badge on the
 * matching `ProviderRow`(s); `dedupMessage` shows the last action's transient
 * result. The panel itself never computes drift or calls the API — it only
 * re-emits `dedupProviders`/`dedupeFiles` for the screen to handle.
 *
 * "Remove fractional files" (→ `removeFractional`, opening the page's
 * `FractionalCleanupDialog`) sits beside "Remove duplicate files" but is a
 * DIFFERENT job: dedupe-files sweeps ORPHAN CBZs (files no chapter owns), while
 * the fractional cleanup removes real downloaded chapter rows + their files. It
 * renders only when `fractionalCleanupCount > 0`.
 */
const props = withDefaults(defineProps<{
  /** The sources to list, importance-descending (preferred first). */
  providers: Provider[]
  /** True while a mutation is in flight — disables reorder + remove. */
  saving?: boolean
  /** SeriesProvider ids flagged as drifted duplicates (drives the banner + per-row badge). */
  driftedIds?: string[]
  /** True while the dedup-providers request is in flight. */
  dedupBusy?: boolean
  /** True while the dedupe-files request is in flight. */
  dedupeFilesBusy?: boolean
  /**
   * How many already-downloaded FRACTIONAL chapters are removable (the
   * server-computed preview's size). 0 HIDES the "Remove fractional files"
   * button entirely — nothing to clean must never present a dead control.
   */
  fractionalCleanupCount?: number
  /** Transient result message from the last dedup / dedupe-files action (null when none). */
  dedupMessage?: string | null
}>(), {
  saving: false,
  driftedIds: () => [],
  dedupBusy: false,
  dedupeFilesBusy: false,
  fractionalCleanupCount: 0,
  dedupMessage: null,
})

const emit = defineEmits<{
  /** A source was re-ranked — carries its id and the direction (-1 up / 1 down). */
  move: [id: string, direction: MoveDirection]
  /** A source removal was requested — carries the SeriesProvider id. */
  removeSource: [id: string]
  /** "Match to source" was pressed on an unlinked disk-origin group — carries its SeriesProvider id. */
  matchProvider: [id: string]
  /** A source's ignore-fractional switch flipped — carries its SeriesProvider id and the NEW value. */
  toggleIgnoreFractional: [id: string, ignore: boolean]
  /** The Add button was pressed (→ opens the Match Source dialog). */
  addSource: []
  /** "Clean up duplicate sources" was pressed. */
  dedupProviders: []
  /** "Remove duplicate files" was pressed. */
  dedupeFiles: []
  /** "Remove fractional files" was pressed (→ opens the page's cleanup dialog). */
  removeFractional: []
}>()

const driftedSet = computed(() => new Set(props.driftedIds))
</script>

<template>
  <PanelCard title="Sources">
    <template #lead>
      <span class="count-pill">{{ providers.length }}</span>
    </template>
    <template #actions>
      <AppButton variant="mini" size="sm" :disabled="dedupeFilesBusy" @click="emit('dedupeFiles')">
        {{ dedupeFilesBusy ? 'Removing…' : 'Remove duplicate files' }}
      </AppButton>
      <!-- Absent when nothing is removable — no dead control (see the prop doc). -->
      <AppButton
        v-if="fractionalCleanupCount > 0"
        variant="mini"
        size="sm"
        @click="emit('removeFractional')"
      >
        Remove fractional files
      </AppButton>
      <AppButton variant="mini" size="sm" @click="emit('addSource')">
        <template #icon>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </template>
        Add
      </AppButton>
    </template>

    <div class="panel__body">
      <div v-if="driftedIds.length > 0" class="dup-banner">
        <span class="dup-banner__text">
          {{ driftedIds.length }} duplicate source{{ driftedIds.length === 1 ? '' : 's' }} detected
        </span>
        <AppButton variant="mini" size="sm" :disabled="dedupBusy" @click="emit('dedupProviders')">
          {{ dedupBusy ? 'Cleaning…' : 'Clean up' }}
        </AppButton>
      </div>
      <div v-if="dedupMessage" class="dup-message">{{ dedupMessage }}</div>

      <div v-if="providers.length > 0" class="panel__eyebrow">Preferred first</div>

      <ProviderRow
        v-for="(p, idx) in providers"
        :key="p.id"
        :provider="p"
        :rank="idx + 1"
        :preferred="idx === 0"
        :can-up="idx !== 0"
        :can-down="idx !== providers.length - 1"
        :saving="saving"
        :duplicate="driftedSet.has(p.id)"
        @move="emit('move', p.id, $event)"
        @remove="emit('removeSource', p.id)"
        @match="emit('matchProvider', p.id)"
        @toggle-ignore-fractional="emit('toggleIgnoreFractional', p.id, $event)"
      />

      <div v-if="providers.length === 0" class="panel__empty">
        No sources tracked. The series stays in your library.
      </div>
    </div>
  </PanelCard>
</template>

<style scoped>
.count-pill {
  padding: 1px 8px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.panel__body {
  padding: 12px;
}

.panel__eyebrow {
  margin: 2px 4px 9px;
  font-size: 10.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--faint);
}

.panel__empty {
  padding: 24px 12px;
  text-align: center;
  font-size: 12.5px;
  color: var(--faint);
}

.dup-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin: 0 4px 10px;
  padding: 8px 11px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
}

.dup-banner__text {
  font-size: 12px;
  font-weight: var(--weight-bold);
  color: var(--danger-text);
}

.dup-message {
  margin: 0 4px 10px;
  font-size: 11.5px;
  color: var(--muted);
}
</style>
