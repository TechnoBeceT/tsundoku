<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import CoverImage from '../ui/CoverImage.vue'
import type { TrackSearchResult } from '../screens/seriesDetail.types'

/**
 * TrackerSearchResultCard — one "Add tracking" search hit, Komikku-style rich
 * card: `CoverImage` + title + `Type · Started · Status` meta line +
 * community-average score + a description snippet, plus a Bind action.
 * `type`/`startDate`/`score`/`description` are BEST-EFFORT (the tracker's own
 * search response may leave any of them at "" / 0 — see `TrackSearchResult`'s
 * doc comment) so every line is conditionally rendered; a result carrying
 * none of them still degrades to a thin title-only card (never a broken
 * layout), which is what `Thin`/`NoCover` cover in the story file.
 *
 *   - `result`: the search hit to render.
 *   - `busy` (default false): true while THIS card's bind is in flight
 *     (mirrors the parent's single shared `binding` flag — every card spins
 *     together, since only one bind request is ever in flight at a time).
 *
 * Emits `bind` (no payload — the parent already knows this card's `remoteId`
 * via `result`).
 */
const props = withDefaults(defineProps<{
  /** The search hit to render. */
  result: TrackSearchResult
  /** True while a bind request is in flight. */
  busy?: boolean
}>(), {
  busy: false,
})

const emit = defineEmits<{
  /** Bind was pressed for this result. */
  bind: []
}>()

// "MANGA · 2018 · RELEASING" — only the parts the tracker actually populated.
const metaLine = computed(() =>
  [props.result.type, props.result.startDate, props.result.status].filter(Boolean).join(' · '))
</script>

<template>
  <div class="result-card">
    <CoverImage
      :src="result.coverUrl"
      :alt="`${result.title} cover`"
      class="result-card__cover"
      aspect="0.7"
      :mark-size="20"
    />
    <div class="result-card__body">
      <p class="result-card__title">{{ result.title }}</p>
      <p v-if="metaLine" class="result-card__meta">{{ metaLine }}</p>
      <p v-if="result.score > 0" class="result-card__score">
        <Icon name="lucide:star" />
        {{ result.score }}
      </p>
      <p v-if="result.description" class="result-card__desc">{{ result.description }}</p>
    </div>
    <AppButton size="sm" variant="mini" :loading="busy" @click="emit('bind')">Bind</AppButton>
  </div>
</template>

<style scoped>
.result-card {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
}

.result-card__cover {
  width: 46px;
  flex: none;
}

.result-card__body {
  min-width: 0;
  flex: 1;
}

.result-card__title {
  margin: 0;
  font-weight: var(--weight-semibold);
  font-size: 13px;
  color: var(--text);
  overflow-wrap: anywhere;
}

.result-card__meta {
  margin: 3px 0 0;
  font-size: var(--text-xs);
  color: var(--muted);
}

.result-card__score {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 3px 0 0;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--accentBright);
}

.result-card__desc {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 4px 0 0;
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--faint);
}

@media (max-width: 900px) {
  .result-card {
    align-items: center;
  }
}
</style>
