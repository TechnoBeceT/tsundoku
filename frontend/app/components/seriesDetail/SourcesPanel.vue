<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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
 * PanelCard itself owns the scroll (`.panel__content`); this panel passes the
 * QCAT-265 treatment #1 `max-height="580px"` bound (the prototype's own value)
 * so a long source list scrolls INTERNALLY. When short (the common 4-7 cards)
 * it simply grows to its content and never hits the bound — a bound is not a
 * letterbox (§2.6.3). Bounding BOTH Chapters and Sources is the owner-ratified
 * asymmetric-pair scope (§2.6.2), so neither dooms the other's reachability.
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
 * "Remove duplicate files" (→ `dedupeFiles`) now opens a preview→confirm dialog
 * (the page's `DedupeCleanupDialog`): the click first fetches the dry-run plan,
 * so `dedupeFilesBusy` covers BOTH the preview fetch and the eventual removal
 * (label "Working…"). "Remove fractional files" (→ `removeFractional`, opening the
 * page's `FractionalCleanupDialog`) sits beside it but is a DIFFERENT job:
 * dedupe-files sweeps ORPHAN CBZs (files no chapter owns) + engine-switch/ignored
 * rows, while the fractional cleanup removes real downloaded chapter rows + their
 * files. It renders only when `fractionalCleanupCount > 0`.
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
  /** True while the dedupe-files FLOW is busy — the preview fetch OR the removal POST (button shows "Working…"). */
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
  /** "Merge N into…" was pressed — carries the SELECTED SeriesProvider ids to consolidate (→ opens the page's target picker). */
  startConsolidate: [providerIds: string[]]
}>()

const driftedSet = computed(() => new Set(props.driftedIds))

// ---- Multi-select consolidation (QCAT-295 Part B) --------------------------
// Selection is a UI concern owned locally: the panel offers checkboxes only when
// there are ≥2 sources to consolidate, and surfaces a "Merge N into…" action once
// ≥1 is ticked. The actual target picker + endpoint call live on the page.
const selectable = computed(() => props.providers.length >= 2)
const selected = ref<Set<string>>(new Set())

// Prune the selection whenever the provider list changes (a completed
// consolidation refetches and drops the folded-away rows) so a stale id never
// lingers in the payload.
watch(
  () => props.providers.map((p) => p.id),
  (ids) => {
    const present = new Set(ids)
    for (const id of [...selected.value]) {
      if (!present.has(id)) selected.value.delete(id)
    }
    // Trigger reactivity for the derived selectedIds (Set mutation is in-place).
    selected.value = new Set(selected.value)
  },
)

const selectedIds = computed(() => [...selected.value])

const toggleSelect = (id: string, isSelected: boolean): void => {
  if (isSelected) selected.value.add(id)
  else selected.value.delete(id)
  selected.value = new Set(selected.value)
}

const clearSelection = (): void => {
  selected.value = new Set()
}

const onStartConsolidate = (): void => {
  if (selectedIds.value.length === 0) return
  emit('startConsolidate', selectedIds.value)
}
</script>

<template>
  <PanelCard title="Sources" max-height="580px">
    <template #lead>
      <span class="count-pill">{{ providers.length }}</span>
    </template>
    <template #actions>
      <AppButton variant="mini" size="sm" :disabled="dedupeFilesBusy" @click="emit('dedupeFiles')">
        {{ dedupeFilesBusy ? 'Working…' : 'Remove duplicate files' }}
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

      <!-- Multi-select merge bar: appears once ≥1 source is ticked. -->
      <div v-if="selectedIds.length > 0" class="merge-bar">
        <span class="merge-bar__text">
          {{ selectedIds.length }} source{{ selectedIds.length === 1 ? '' : 's' }} selected
        </span>
        <div class="merge-bar__actions">
          <AppButton variant="mini" size="sm" @click="clearSelection">Clear</AppButton>
          <AppButton variant="primary" size="sm" :disabled="saving" @click="onStartConsolidate">
            Merge into…
          </AppButton>
        </div>
      </div>

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
        :selectable="selectable"
        :selected="selected.has(p.id)"
        @move="emit('move', p.id, $event)"
        @remove="emit('removeSource', p.id)"
        @match="emit('matchProvider', p.id)"
        @toggle-ignore-fractional="emit('toggleIgnoreFractional', p.id, $event)"
        @toggle-select="toggleSelect(p.id, $event)"
      />

      <div v-if="providers.length === 0" class="panel__empty">
        No sources tracked. The series stays in your library.
      </div>
    </div>
  </PanelCard>
</template>

<style scoped>
/* Off-ladder px migrated to byte-identical rem literals (value÷16) so they
 * scale with the fluid root on a phone yet resolve to their exact design px at
 * the desktop anchor; exact-match values use the spacing/text tokens. */
.count-pill {
  padding: 1px var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.panel__body {
  padding: var(--space-md);
}

.panel__eyebrow {
  margin: var(--space-3xs) var(--space-2xs) 0.5625rem;
  font-size: 0.65625rem;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--faint);
}

.panel__empty {
  padding: var(--space-2xl) var(--space-md);
  text-align: center;
  font-size: 0.78125rem;
  color: var(--faint);
}

.dup-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm);
  margin: 0 var(--space-2xs) var(--space-sm);
  padding: var(--space-xs) 0.6875rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
}

.dup-banner__text {
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  color: var(--danger-text);
}

.dup-message {
  margin: 0 var(--space-2xs) var(--space-sm);
  font-size: 0.71875rem;
  color: var(--muted);
}

/* The multi-select merge bar — accent-tinted so it reads as an active batch action. */
.merge-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm);
  margin: 0 var(--space-2xs) var(--space-sm);
  padding: var(--space-xs) 0.6875rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--accent);
  background: var(--accentSoft);
}

.merge-bar__text {
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.merge-bar__actions {
  display: flex;
  gap: var(--space-xs);
}
</style>
