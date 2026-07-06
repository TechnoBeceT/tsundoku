<script setup lang="ts">
import PanelCard from './PanelCard.vue'
import AppButton from '../ui/AppButton.vue'
import ProviderRow from './ProviderRow.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'
import type { ScanlatorCoverage } from '../screens/import.types'

/**
 * SourcesPanel — the Series-Detail "Sources" card: a titled header with the
 * source-count pill and an Add button, then the importance-ranked `ProviderRow`
 * list (preferred first) or an empty note when nothing is tracked.
 * Presentation-only — the (already-sorted) providers arrive via props; the panel
 * re-emits each row's `move`/`remove`/`match`/`loadCoverage` (keyed by source id)
 * plus `addSource`. Wraps the shared PanelCard shell: the count pill rides the
 * header-left `lead` slot (grouped with the title), the Add button the
 * header-right `actions` slot, and the provider list the full-bleed body.
 *
 * `providerCoverage` is a passthrough map (SeriesProvider id → that row's
 * cached coverage, or `undefined`/absent when never fetched) — this panel
 * never fetches anything itself; it only threads each row's slice of the map
 * down to `ProviderRow` and bubbles the row's `loadCoverage` click back up to
 * the screen (which owns the actual lazy fetch, `useSeriesDetail.
 * loadProviderCoverage`).
 */
withDefaults(defineProps<{
  /** The sources to list, importance-descending (preferred first). */
  providers: Provider[]
  /** True while a mutation is in flight — disables reorder + remove. */
  saving?: boolean
  /** Per-provider cached coverage, keyed by SeriesProvider id (see doc above). */
  providerCoverage?: Record<string, ScanlatorCoverage[] | null>
}>(), {
  saving: false,
  providerCoverage: () => ({}),
})

const emit = defineEmits<{
  /** A source was re-ranked — carries its id and the direction (-1 up / 1 down). */
  move: [id: string, direction: MoveDirection]
  /** A source removal was requested — carries the SeriesProvider id. */
  removeSource: [id: string]
  /** "Match to source" was pressed on an unlinked disk-origin group — carries its SeriesProvider id. */
  matchProvider: [id: string]
  /** A row's "Show coverage" button was pressed — carries its SeriesProvider id. */
  loadCoverage: [id: string]
  /** The Add button was pressed (→ opens the Match Source dialog). */
  addSource: []
}>()
</script>

<template>
  <PanelCard title="Sources">
    <template #lead>
      <span class="count-pill">{{ providers.length }}</span>
    </template>
    <template #actions>
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
        :coverage="providerCoverage[p.id]"
        @move="emit('move', p.id, $event)"
        @remove="emit('removeSource', p.id)"
        @match="emit('matchProvider', p.id)"
        @load-coverage="emit('loadCoverage', p.id)"
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
</style>
