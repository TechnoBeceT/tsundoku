<script setup lang="ts">
import { computed } from 'vue'
import CoverImage from '../ui/CoverImage.vue'
import UnhealthySourceRow from './UnhealthySourceRow.vue'
import type { SeriesHealth } from '../screens/libraryHealth.types'

/**
 * SickSeriesCard — one sick series in the Library Health report: a clickable
 * header (cover · title · "N unhealthy sources") followed by a list of that
 * series' unhealthy sources (one UnhealthySourceRow each). Presentation-only —
 * the series arrives via props; clicking the header emits `open-series`.
 *
 * Token-only colours, so it renders correctly in both themes.
 */
const props = defineProps<{
  /** The sick series to render (title + its unhealthy sources). */
  series: SeriesHealth
}>()

const emit = defineEmits<{
  /** The header was clicked — open this series' detail view. */
  'open-series': [seriesId: string]
}>()

// "N unhealthy source(s)" — pluralised on the source count.
const sourceLabel = computed(() => {
  const n = props.series.sources.length
  return `${n} unhealthy source${n > 1 ? 's' : ''}`
})
</script>

<template>
  <div class="card">
    <button
      type="button"
      class="card__head"
      :aria-label="`Open ${series.title}`"
      @click="emit('open-series', series.id)"
    >
      <span class="card__cover">
        <CoverImage :alt="series.title" placeholder="initial" aspect="0.777" />
      </span>
      <span class="card__titles">
        <span class="card__title">{{ series.title }}</span>
        <span class="card__sub">{{ sourceLabel }}</span>
      </span>
      <svg class="card__chevron" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M9 18l6-6-6-6" />
      </svg>
    </button>

    <UnhealthySourceRow
      v-for="src in series.sources"
      :key="src.id"
      :source="src"
    />
  </div>
</template>

<style scoped>
/* px→rem (§5.16 preserve≠skip-responsive): this card has NO content-out
 * legibility break across its width band (cover is a small fixed 42px, all text
 * is on `--text-*` tokens that ride the fluid root and either ellipsize or wrap),
 * so it correctly takes NO §3 `@container` step — only the px→rem migration.
 * Every literal below is byte-identical at the 16px desktop anchor (value ÷ 16);
 * the 1px hairline border stays px. */
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: var(--space-lg); /* 16px @16 */
}

.card__head {
  display: flex;
  align-items: center;
  gap: 0.8125rem; /* 13px @16 — off-ladder, byte-identical rem literal */
  width: 100%;
  margin-bottom: 0.8125rem; /* 13px @16 — off-ladder, byte-identical rem literal */
  padding: 0;
  border: none;
  background: none;
  text-align: left;
  cursor: pointer;
}

.card__cover {
  width: 2.625rem; /* 42px @16 — byte-identical rem literal */
  border-radius: var(--radius-sm);
  overflow: hidden;
  flex: none;
}

.card__titles {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs); /* 2px @16 */
}

.card__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card__sub {
  font-size: var(--text-xs);
  color: var(--faint);
}

.card__chevron {
  flex: none;
  color: var(--faint);
}
</style>
