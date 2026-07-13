<script setup lang="ts">
import Chip from '../ui/Chip.vue'
import CoverImage from '../ui/CoverImage.vue'
import type { MetadataCandidate } from '../screens/seriesDetail.types'

/**
 * MetadataCandidateCard — one selectable search result in the "Identify" match
 * flow: a portrait cover, the provider's title (2-line clamp), and a provider
 * badge (AniList / MAL / MangaDex / MangaUpdates) with the year. The whole card
 * is a single `<button>`, so it is keyboard-selectable and carries a visible
 * focus ring; `aria-pressed` exposes the selected state to assistive tech.
 *
 * Selected = the accent ring/border + a check badge on the cover. Presentation-
 * only: the candidate + selected flag arrive via props, a pick emits `select`.
 */
defineProps<{
  /** The search result to render. */
  candidate: MetadataCandidate
  /** Whether this card is the currently-chosen candidate. */
  selected: boolean
}>()

const emit = defineEmits<{
  /** The card was chosen (click or keyboard). */
  select: []
}>()
</script>

<template>
  <button
    type="button"
    class="cand"
    :class="{ 'cand--selected': selected }"
    :aria-pressed="selected"
    @click="emit('select')"
  >
    <span class="cand__cover">
      <CoverImage
        :src="candidate.coverUrl"
        :alt="`${candidate.title} cover`"
        placeholder="initial"
        :initial="candidate.title"
        radius="var(--radius-md)"
      />
      <span v-if="selected" class="cand__check" aria-hidden="true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 6 9 17l-5-5" />
        </svg>
      </span>
    </span>

    <span class="cand__body">
      <span class="cand__title">{{ candidate.title }}</span>
      <span class="cand__meta">
        <Chip :variant="selected ? 'accent' : 'neutral'">{{ candidate.provider }}</Chip>
        <span v-if="candidate.year !== undefined" class="cand__year">{{ candidate.year }}</span>
      </span>
    </span>
  </button>
</template>

<style scoped>
.cand {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 8px;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--border);
  background: var(--surface2);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s, box-shadow 0.15s;
}

.cand:hover {
  border-color: var(--border2);
}

.cand--selected {
  border-color: var(--accent);
  background: var(--accentSoft);
  box-shadow: 0 0 0 3px var(--accentSoft);
}

.cand:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

/* ---- Cover ---------------------------------------------------------------- */
.cand__cover {
  position: relative;
  display: block;
}

/* The accent check badge, top-right on the cover, for the selected card. */
.cand__check {
  position: absolute;
  top: 6px;
  right: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--accent);
  color: var(--cover-text);
  box-shadow: var(--shadow-accent-sm);
}

/* ---- Body ----------------------------------------------------------------- */
.cand__body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.cand__title {
  /* 2-line clamp — long provider titles never blow out the card height. */
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  line-height: 1.32;
  color: var(--text);
}

.cand__meta {
  display: flex;
  align-items: center;
  gap: 7px;
}

.cand__year {
  font-family: var(--font-mono);
  font-size: 10px;
  color: var(--faint);
}
</style>
