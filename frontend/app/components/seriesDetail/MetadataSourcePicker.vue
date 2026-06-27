<script setup lang="ts">
import BrandMark from '../ui/BrandMark.vue'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * MetadataSourcePicker — the (PLANNED) Series-Detail card for choosing which
 * source supplies the displayed title + cover. One selectable card per source;
 * the active card carries the accent border + check. Presentation-only — the
 * sources, the series title, and the active/preferred ids arrive via props; a
 * pick emits `pick` with the chosen SeriesProvider id.
 */
const props = defineProps<{
  /** The sources to offer, importance-descending (preferred first). */
  providers: Provider[]
  /** The series title shown on every card. */
  title: string
  /** The currently-active source id (pinned, else the preferred one). */
  activeId: string | null
  /** The preferred (rank-1) source id — its card reads "Preferred · default". */
  preferredId: string | null
  /** True while a mutation is in flight — disables the cards. */
  saving?: boolean
}>()

const emit = defineEmits<{
  /** A source was picked — carries its SeriesProvider id. */
  pick: [id: string]
}>()

// The tag under each card: the preferred source is the default; others show rank.
const tag = (p: Provider): string =>
  p.id === props.preferredId ? 'Preferred · default' : `importance ${p.importance}`
</script>

<template>
  <section class="panel meta">
    <div class="meta__head">
      <span class="panel__title">Metadata source</span>
      <span class="chip-planned">PLANNED</span>
    </div>
    <p class="meta__desc">
      Which source supplies the title &amp; cover shown across the app. Defaults to the preferred source.
    </p>
    <div class="meta__cards">
      <button
        v-for="p in providers"
        :key="p.id"
        type="button"
        class="metacard"
        :class="{ 'metacard--active': p.id === activeId }"
        :disabled="saving"
        @click="emit('pick', p.id)"
      >
        <span class="metacard__cover">
          <BrandMark :size="22" tone="inverse" />
        </span>
        <span class="metacard__body">
          <span class="metacard__name">
            {{ p.provider }}
            <svg v-if="p.id === activeId" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" class="metacard__check" aria-hidden="true">
              <path d="M20 6L9 17l-5-5" />
            </svg>
          </span>
          <span class="metacard__title">{{ title }}</span>
          <span class="metacard__tag">{{ tag(p) }}</span>
        </span>
      </button>
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

.panel__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 15px;
  color: var(--text);
}

.meta {
  margin-bottom: 18px;
  padding: 16px 18px;
}

.meta__head {
  display: flex;
  align-items: center;
  gap: 9px;
  margin-bottom: 4px;
}

/* The "PLANNED" marker — a quieter, smaller pill than the shared Chip atom. */
.chip-planned {
  display: inline-flex;
  align-items: center;
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  line-height: 1.7;
  white-space: nowrap;
  background: var(--surface3);
  color: var(--faint);
}

.meta__desc {
  margin: 0 0 13px;
  font-size: var(--text-sm);
  color: var(--faint);
}

.meta__cards {
  display: flex;
  gap: 11px;
  flex-wrap: wrap;
}

.metacard {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 210px;
  padding: 9px 11px;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--border);
  background: var(--surface2);
  cursor: pointer;
  text-align: left;
  transition: all 0.15s;
}

.metacard--active {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.metacard:disabled {
  cursor: default;
  opacity: 0.7;
}

.metacard__cover {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 54px;
  flex: none;
  border-radius: 7px;
  background: var(--cover-placeholder);
}

.metacard__body {
  min-width: 0;
}

.metacard__name {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 2px;
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.metacard__check {
  color: var(--accentBright);
  flex: none;
}

.metacard__title {
  display: block;
  max-width: 160px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: var(--text-xs);
  color: var(--muted);
}

.metacard__tag {
  display: block;
  margin-top: 2px;
  font-size: 10px;
  color: var(--faint);
}
</style>
