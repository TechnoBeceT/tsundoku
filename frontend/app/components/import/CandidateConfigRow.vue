<script setup lang="ts">
import CoverImage from '../ui/CoverImage.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import Spinner from '../ui/Spinner.vue'
import ChapterInspectList from './ChapterInspectList.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { ChapterInspect, SearchCandidate } from '../screens/import.types'

/**
 * CandidateConfigRow — one selectable, rankable source row in Stage 2
 * (Configure): a select checkbox, a tiny cover (<CoverImage>), the source name +
 * language, an Inspect button, and — when selected — the up/down rank stepper
 * (<ReorderControl>, higher rank = higher importance). Below the row sits the
 * inspect preview (§16): a loading spinner while `inspecting`, then the resolved
 * <ChapterInspectList> once `inspected`.
 *
 * Presentation-only — the candidate + its row state arrive via props; the row
 * emits `toggle` (select), `inspect` (load chapters), and `move` (re-rank).
 *
 * The tiny cover reuses <CoverImage> with the row's small corner via the public
 * `radius` prop; only the smaller initial-glyph size still needs a scoped
 * `:deep` override (CoverImage exposes no prop for the initial-letter size).
 */
defineProps<{
  /** The candidate this row represents. */
  candidate: SearchCandidate
  /** Whether this candidate is selected for adoption. */
  selected: boolean
  /** This candidate's 1-based rank among the selected set (drives importance). */
  rank: number
  /** Whether the rank can move up (false = already top of the selected set). */
  canUp: boolean
  /** Whether the rank can move down (false = already bottom of the selected set). */
  canDown: boolean
  /** True while this row's chapter inspect is in flight (show the spinner). */
  inspecting: boolean
  /** True once this row's chapters have resolved (show the list). */
  inspected: boolean
  /** The resolved chapter-preview rows (rendered when `inspected`). */
  chapters: ChapterInspect[]
}>()

const emit = defineEmits<{
  /** Toggle this candidate's selection. */
  toggle: []
  /** Load this candidate's chapter list (Stage 2 inspect). */
  inspect: []
  /** Re-rank this candidate: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
}>()
</script>

<template>
  <div class="cand" :class="{ 'cand--on': selected }">
    <div class="cand__row">
      <button
        type="button"
        class="check"
        :class="{ 'check--on': selected }"
        :aria-pressed="selected"
        :aria-label="`Toggle ${candidate.sourceName}`"
        @click="emit('toggle')"
      >
        <svg v-if="selected" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 6L9 17l-5-5" />
        </svg>
      </button>

      <span class="cand__cover">
        <CoverImage
          :src="candidate.thumbnailUrl"
          :alt="`${candidate.title} cover`"
          placeholder="initial"
          :initial="candidate.title"
          aspect="30 / 40"
          radius="var(--radius-xs)"
        />
      </span>

      <span class="cand__meta">
        <span class="cand__source">{{ candidate.sourceName }}</span>
        <span class="cand__lang">{{ candidate.lang.toUpperCase() }}</span>
      </span>

      <button type="button" class="inspect" @click="emit('inspect')">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
          <circle cx="12" cy="12" r="3" />
        </svg>
        Inspect
      </button>

      <ReorderControl
        v-if="selected"
        :can-up="canUp"
        :can-down="canDown"
        :rank="rank"
        :top-highlighted="rank === 1"
        @move="emit('move', $event)"
      />
    </div>

    <!-- Inspect preview: loading spinner → chapter list (§16) -->
    <div v-if="inspecting" class="inspect-loading">
      <Spinner :size="16" tone="accent" />
      Loading chapters…
    </div>
    <ChapterInspectList v-else-if="inspected" :chapters="chapters" />
  </div>
</template>

<style scoped>
.cand {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 13px;
  margin-bottom: 10px;
  background: var(--surface2);
  transition: all 0.15s;
}

.cand--on {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.cand__row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.check {
  width: 22px;
  height: 22px;
  border-radius: var(--radius-xs);
  border: 1.5px solid var(--border2);
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex: none;
  color: var(--cover-text);
  padding: 0;
}

.check--on {
  border-color: var(--accent);
  background: var(--accent);
}

.cand__cover {
  width: 30px;
  flex: none;
}

/* The row's small radius now rides CoverImage's `radius` prop; only the smaller
   initial glyph still needs a tune (CoverImage has no prop for its size). */
.cand__cover :deep(.cover__initial) {
  font-size: var(--text-xl);
}

.cand__meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.cand__source {
  font-size: 13.5px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.cand__lang {
  font-size: var(--text-xs);
  color: var(--faint);
}

.inspect {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  cursor: pointer;
  flex: none;
  transition: color 0.15s, border-color 0.15s;
}

.inspect:hover {
  color: var(--accentBright);
  border-color: var(--accent);
}

.inspect-loading {
  margin-top: 11px;
  padding: 11px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
  font-size: var(--text-base);
}
</style>
