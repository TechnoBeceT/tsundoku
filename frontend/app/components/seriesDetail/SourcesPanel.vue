<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'
import ProviderRow from './ProviderRow.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * SourcesPanel — the Series-Detail "Sources" card: a titled header with the
 * source-count pill and an Add button, then the importance-ranked `ProviderRow`
 * list (preferred first) or an empty note when nothing is tracked.
 * Presentation-only — the (already-sorted) providers arrive via props; the panel
 * re-emits each row's `move`/`remove` (keyed by source id) plus `addSource`.
 */
defineProps<{
  /** The sources to list, importance-descending (preferred first). */
  providers: Provider[]
  /** True while a mutation is in flight — disables reorder + remove. */
  saving?: boolean
}>()

const emit = defineEmits<{
  /** A source was re-ranked — carries its id and the direction (-1 up / 1 down). */
  move: [id: string, direction: MoveDirection]
  /** A source removal was requested — carries the SeriesProvider id. */
  removeSource: [id: string]
  /** The Add button was pressed (→ the import flow). */
  addSource: []
}>()
</script>

<template>
  <section class="panel">
    <div class="panel__head">
      <div class="panel__headleft">
        <span class="panel__title">Sources</span>
        <span class="count-pill">{{ providers.length }}</span>
      </div>
      <AppButton variant="mini" size="sm" @click="emit('addSource')">
        <template #icon>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </template>
        Add
      </AppButton>
    </div>

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
        @move="emit('move', p.id, $event)"
        @remove="emit('removeSource', p.id)"
      />

      <div v-if="providers.length === 0" class="panel__empty">
        No sources tracked. The series stays in your library.
      </div>
    </div>
  </section>
</template>

<style scoped>
.panel {
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
  min-width: 0;
}

.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 9px;
  padding: 15px 18px;
  border-bottom: 1px solid var(--border);
}

.panel__headleft {
  display: flex;
  align-items: center;
  gap: 9px;
}

.panel__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 15px;
  color: var(--text);
}

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
