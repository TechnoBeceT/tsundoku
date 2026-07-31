<script setup lang="ts">
import { computed } from 'vue'
import CoverImage from '../ui/CoverImage.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import Spinner from '../ui/Spinner.vue'
import ChapterInspectList from './ChapterInspectList.vue'
import { safeHttpUrl } from '../../utils/safeUrl'
import { relativeTime } from '../../utils/timeFormat'
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
 *
 * `hideInspect`/`hideReorder` are opt-in escape hatches. `hideInspect` is set by
 * `SourceConfigurePanel` (the shared Configure block behind Adopt / Add-source /
 * Import-match) for split-scanlator rows + surfaces with no live inspect endpoint.
 * `hideReorder` is set by the single-select `seriesDetail/MatchDiskProviderDialog`
 * (the no-re-download Match: pick exactly one source+scanlator for a disk group —
 * nothing to rank). Both default `false` so the real Adopt wizard
 * (`screens/Import.vue`, multi-source ranking + live inspect) renders unchanged.
 * (The old single-select `MatchPanel`/`MatchSourceDialog` are now multi-select,
 * ranked surfaces that render this row via `SourceConfigurePanel`.)
 *
 * `scanlator`/`chapterCount`/`chapterRanges`/`coverageUnavailable` are a
 * second set of opt-in props (all default off/empty/undefined) driving the
 * Adopt wizard's per-scanlator auto-split rows (mirrors Kaizoku.GO's
 * `ConfirmSeriesStep.vue` provider-info line): a subtitle naming the
 * scanlation group this row tracks, and an inline "N chapters · ranges"
 * coverage line. Left unset, the row renders exactly as before — the two
 * match surfaces never pass these.
 *
 * `coverageStatus`/`coverageComputedAt`/`coverageError` (GAP-140) layer the
 * async breakdown snapshot's own lifecycle on top of that coverage line —
 * `pending` (a background walk is still running), `ready` (the counts above
 * are trustworthy, dated by `coverageComputedAt`), or `failed`
 * (`coverageError` names the reason). These are independent of the existing
 * `coverageUnavailable` flag, which single-shot callers still set directly on
 * a synchronous failure — `coverageStatus === 'failed'` renders identically.
 * A caller that never resolves this snapshot leaves `coverageStatus` unset,
 * so nothing here changes for it.
 *
 * A `ready` or `failed` snapshot ALSO shows a small refresh control (GAP-140
 * follow-up) — a `ready` snapshot's counts are otherwise frozen at the first
 * computation forever, and a `failed` one otherwise waits out its own cooldown
 * before anyone can retry it. `pending` shows no control (the backend never
 * duplicates a walk already in flight, so a refresh click on a pending row
 * would be a no-op — better to just hide it) and neither does an unset
 * `coverageStatus` (a caller that doesn't track the snapshot has nothing to
 * refresh). Presentation-only: the row only EMITS `refresh`; the consumer's
 * data layer owns the actual re-fetch (§16 — only the composition root learns
 * whether it succeeded).
 */
const props = withDefaults(defineProps<{
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
  /** Hide the Inspect button — for surfaces with no live chapter-inspect endpoint. */
  hideInspect?: boolean
  /** Never render the reorder stepper — for single-select surfaces with nothing to rank. */
  hideReorder?: boolean
  /** Scanlation group subtitle shown under the source name; "" hides it. */
  scanlator?: string
  /** Chapter count for this row's coverage; omit to show no coverage line. */
  chapterCount?: number
  /** Human-readable chapter-range string (e.g. "1-90, 92-101"), appended when non-empty. */
  chapterRanges?: string
  /** True when the source's breakdown fetch failed — shows a "Coverage unavailable" note. */
  coverageUnavailable?: boolean
  /** Lifecycle of the async breakdown snapshot (GAP-140); unset renders exactly as before. */
  coverageStatus?: 'ready' | 'pending' | 'failed'
  /** As-of instant of the ready snapshot; rendered alongside the counts so a persisted result is never mistaken for a fresh one. */
  coverageComputedAt?: string
  /** Human-readable reason the last computation failed. */
  coverageError?: string
}>(), {
  hideInspect: false,
  hideReorder: false,
  scanlator: '',
  chapterCount: undefined,
  chapterRanges: '',
  coverageUnavailable: false,
  coverageStatus: undefined,
  coverageComputedAt: '',
  coverageError: '',
})

const emit = defineEmits<{
  /** Toggle this candidate's selection. */
  toggle: []
  /** Load this candidate's chapter list (Stage 2 inspect). */
  inspect: []
  /** Re-rank this candidate: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
  /** Force a recomputation of this row's breakdown snapshot (GAP-140). */
  refresh: []
}>()

// The source name links to the candidate's browser-clickable realUrl, opening
// in a new tab. Scheme-guarded via the shared safeHttpUrl (untrusted upstream
// source data) → undefined when there is no valid link, so the name renders as
// plain, non-clickable text instead of the source-relative addressing url.
const sourceHref = computed(() => safeHttpUrl(props.candidate.realUrl))
</script>

<template>
  <div class="cand" :class="{ 'cand--on': selected }">
    <div class="cand__row">
      <!-- Lead group (select + cover + name/coverage): stays together and takes
           the full row width on mobile so the meta text never gets crushed into
           a sliver beside the trailing controls (QCAT-230). -->
      <div class="cand__lead">
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
          <a
            v-if="sourceHref"
            class="cand__source cand__source--link"
            :href="sourceHref"
            target="_blank"
            rel="noopener noreferrer"
          >{{ candidate.sourceName }}</a>
          <span v-else class="cand__source">{{ candidate.sourceName }}</span>
          <span v-if="scanlator" class="cand__scanlator">{{ scanlator }}</span>
          <span class="cand__lang">{{ candidate.lang.toUpperCase() }}</span>
          <span v-if="coverageStatus === 'pending'" class="cand__coverage cand__coverage--muted">
            Computing coverage…
          </span>
          <span v-else-if="coverageUnavailable || coverageStatus === 'failed'" class="cand__coverage cand__coverage--muted">
            Coverage unavailable<span v-if="coverageError"> — {{ coverageError }}</span>
            <button
              v-if="coverageStatus === 'failed'"
              type="button"
              class="coverage-refresh"
              :aria-label="`Refresh coverage for ${candidate.sourceName}`"
              @click.stop="emit('refresh')"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M21 12a9 9 0 1 1-2.64-6.36" />
                <path d="M21 3v6h-6" />
              </svg>
            </button>
          </span>
          <span v-else-if="chapterCount != null" class="cand__coverage">
            {{ chapterCount }} chapter{{ chapterCount === 1 ? '' : 's' }}<span v-if="chapterRanges"> · {{ chapterRanges }}</span><span v-if="coverageComputedAt" class="cand__asof"> · as of {{ relativeTime(coverageComputedAt) }}</span>
            <button
              v-if="coverageStatus === 'ready'"
              type="button"
              class="coverage-refresh"
              :aria-label="`Refresh coverage for ${candidate.sourceName}`"
              @click.stop="emit('refresh')"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M21 12a9 9 0 1 1-2.64-6.36" />
                <path d="M21 3v6h-6" />
              </svg>
            </button>
          </span>
        </span>
      </div>

      <!-- Trailing group (inspect + rank): wraps onto its own right-aligned row
           on mobile instead of squeezing the lead group down (QCAT-230). -->
      <div class="cand__trail">
        <button v-if="!hideInspect" type="button" class="inspect" @click="emit('inspect')">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          Inspect
        </button>

        <ReorderControl
          v-if="selected && !hideReorder"
          :can-up="canUp"
          :can-down="canDown"
          :rank="rank"
          :top-highlighted="rank === 1"
          @move="emit('move', $event)"
        />
      </div>
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
  padding: 0.6875rem 0.8125rem; /* 11px 13px @16 — off-ladder, byte-identical */
  margin-bottom: var(--space-sm); /* 10px @16 */
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
  gap: var(--space-md); /* 12px @16 */
}

/* Lead (select+cover+meta) grows to fill the row; trail (inspect+rank) stays
 * its natural width — matches the pre-split single-line layout at desktop
 * width. See the `@media` override below for the mobile stack. */
.cand__lead {
  display: flex;
  align-items: center;
  gap: var(--space-md); /* 12px @16 */
  flex: 1;
  min-width: 0;
}

.cand__trail {
  display: flex;
  align-items: center;
  gap: var(--space-md); /* 12px @16 */
  flex: none;
}

@media (max-width: 900px) {
  /* Force the trailing controls (Inspect + rank stepper) onto their OWN
   * right-aligned row below the lead group, rather than letting the row's
   * natural flex-shrink squeeze the source name/coverage text into an
   * unreadable sliver (QCAT-230 — CandidateConfigRow crushed on mobile). */
  .cand__row {
    flex-wrap: wrap;
  }

  .cand__lead {
    flex: 1 1 100%;
  }

  .cand__trail {
    flex: 1 1 100%;
    justify-content: flex-end;
  }
}

.check {
  width: var(--control-xs); /* 22px @16 — square control */
  height: var(--control-xs);
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
  width: 1.875rem; /* 30px @16 */
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
  font-size: 0.84375rem; /* 13.5px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-bold);
  color: var(--text);
  overflow-wrap: anywhere;
}

/* When a browser-clickable realUrl exists the source name becomes a link
   (new tab); it stays visually identical until hover, then reads as a link. */
.cand__source--link {
  text-decoration: none;
  width: fit-content;
  cursor: pointer;
  transition: color 0.15s;
}

.cand__source--link:hover {
  color: var(--accentBright);
  text-decoration: underline;
}

.cand__source--link:focus-visible {
  outline: none;
  color: var(--accentBright);
  text-decoration: underline;
}

.cand__lang {
  font-size: var(--text-xs);
  color: var(--faint);
}

.cand__scanlator {
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.cand__coverage {
  font-size: var(--text-xs);
  color: var(--faint);
  margin-top: 0.0625rem; /* 1px @16 */
}

.cand__coverage--muted {
  font-style: italic;
}

.coverage-refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1rem; /* 16px @16 */
  height: 1rem;
  margin-left: 0.25rem; /* 4px @16 */
  border: none;
  background: transparent;
  padding: 0;
  color: var(--faint);
  cursor: pointer;
  vertical-align: -0.125rem;
  transition: color 0.15s;
}

.coverage-refresh:hover,
.coverage-refresh:focus-visible {
  color: var(--accentBright);
  outline: none;
}

.inspect {
  display: flex;
  align-items: center;
  gap: 0.3125rem; /* 5px @16 — off-ladder, byte-identical rem literal */
  padding: var(--space-xs-tight) var(--space-sm); /* 6px 10px @16 */
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
  margin-top: 0.6875rem; /* 11px @16 — off-ladder, byte-identical rem literal */
  padding: 0.6875rem 0.8125rem; /* 11px 13px @16 — off-ladder, byte-identical */
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-sm); /* 10px @16 */
  color: var(--muted);
  font-size: var(--text-base);
}
</style>
