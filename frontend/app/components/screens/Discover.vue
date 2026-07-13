<script setup lang="ts">
import { computed } from 'vue'
import SelectField from '../ui/SelectField.vue'
import SegmentedToggle from '../ui/SegmentedToggle.vue'
import AppButton from '../ui/AppButton.vue'
import Spinner from '../ui/Spinner.vue'
import DiscoverCard from '../discover/DiscoverCard.vue'
import type { SegmentOption } from '../ui/controls.types'
import type { SelectOption } from '../ui/forms.types'
import type { BrowseResult, BrowseType, DiscoverCandidate, DiscoverSource } from './discover.types'

/**
 * Discover — per-source catalog browse: a source picker + Popular/Latest toggle
 * over a cover-forward grid of <DiscoverCard>s, each with a robust hover-preview
 * popup. It is the browse-driven sibling of Search; both feed the SAME Adopt flow.
 *
 * Thin container: ALL data arrives via props and every action is emitted — no
 * fetching, routing, or stores. It composes the shared atoms (SelectField,
 * SegmentedToggle) + the DiscoverCard organism, and references only design
 * tokens, so it reads correctly in both themes.
 *
 * Fixes the two old-Kaizoku Discover bugs (now owned by DiscoverCard):
 *  - Bug 1 (dead navigation): a card click opens the in-app Adopt flow, never a
 *    series-detail route; "View on source ↗" is a real external `<a>`.
 *  - Bug 2 (broken hover popup): the preview is a sibling of the card's clipped
 *    box, `position:absolute`, `pointer-events:none`, and lifts the card's
 *    `z-index` on hover — see DiscoverCard / DiscoverHoverPreview.
 */
const props = withDefaults(defineProps<{
  /** The current (possibly-appended) page of browse results. */
  result: BrowseResult
  /** Sources available to browse (populates the picker). */
  sources: DiscoverSource[]
  /** The active source ID. */
  activeSource: string
  /** The active listing — Popular or Latest. */
  activeType?: BrowseType
  /** When true, a fetch is in flight (initial → skeletons, more → spinner row). */
  loading?: boolean
  /** When true, the active source failed — show the error banner + retry. */
  error?: boolean
}>(), {
  activeType: 'popular',
  loading: false,
  error: false,
})

const emit = defineEmits<{
  /** The owner picked a different source (carries its ID). Refetches page 1. */
  setSource: [sourceId: string]
  /** The owner switched the listing. Refetches page 1. */
  setType: [type: BrowseType]
  /** The owner asked for the next page (carries the 1-based page to load). */
  page: [page: number]
  /** Primary card click — open the Adopt/Inspect flow for this candidate. */
  inspect: [candidate: DiscoverCandidate]
  /** "+ Adopt" click — open the Adopt flow with intent to adopt this candidate. */
  adopt: [candidate: DiscoverCandidate]
  /** "View on source ↗" clicked — the parent may react; the `<a>` still opens. */
  openSourceLink: [candidate: DiscoverCandidate]
  /** Retry the active source after an error — refetches page 1. */
  retry: []
  /** A card was hovered — forwarded verbatim from DiscoverCard; the parent
   *  page debounces this to trigger the on-demand rich-details fetch. */
  hover: [candidate: DiscoverCandidate]
}>()

// The browse result's accumulated candidates (kept terse for the template).
const items = computed(() => props.result.manga)

// State gating mirrors the prototype's vmDiscover exactly. "first load" shows a
// skeleton grid; "load more" shows a spinner row beneath the existing cards.
const isFirstLoad = computed(() => props.loading && items.value.length === 0)
const isLoadingMore = computed(() => props.loading && items.value.length > 0)
const isEmpty = computed(() => !props.loading && !props.error && items.value.length === 0)
const isEnd = computed(() => !props.loading && !props.result.hasNextPage && items.value.length > 0)
const hasMore = computed(() => !props.loading && props.result.hasNextPage && items.value.length > 0)

// Source-picker options: each labelled "Name · LANG".
const sourceOptions = computed<SelectOption[]>(() =>
  props.sources.map(s => ({ value: s.id, label: `${s.name} · ${s.lang.toUpperCase()}` })),
)

// The Popular / Latest segmented toggle's two fixed options.
const typeOptions: SegmentOption[] = [
  { key: 'popular', label: 'Popular' },
  { key: 'latest', label: 'Latest' },
]

// A handful of skeleton placeholders for the first-load grid.
const skeletons = Array.from({ length: 12 }, (_, i) => i)

const loadMore = (): void => emit('page', props.result.page + 1)
</script>

<template>
  <div class="discover">
    <!-- Top: flows naturally, pinned above the scrolling grid region (QCAT-231) -->
    <div class="discover__top">
      <!-- Controls: source picker + Popular/Latest toggle + caption -->
      <div class="discover__controls">
        <div class="discover__source">
          <span class="discover__source-label">Source</span>
          <SelectField
            class="discover__select"
            :model-value="activeSource"
            :options="sourceOptions"
            aria-label="Source"
            @update:model-value="emit('setSource', $event)"
          />
        </div>

        <SegmentedToggle
          class="discover__toggle"
          :model-value="activeType"
          :options="typeOptions"
          @update:model-value="emit('setType', $event as BrowseType)"
        />

        <p class="discover__caption">Browse a source &amp; adopt — covers are the focus</p>
      </div>

      <!-- Error banner (the active source failed) -->
      <div v-if="error" class="discover__error">
        <p class="discover__error-title">Couldn't reach this source</p>
        <p class="discover__error-body">The source returned an error. It may be temporarily down.</p>
        <button type="button" class="retry-btn" @click="emit('retry')">Retry</button>
      </div>
    </div>

    <!-- Scroll region: the grid FITS THE SCREEN and scrolls INSIDE itself
         (QCAT-231) so paging/loading never scrolls the whole page. -->
    <div class="discover__scroll">
      <!-- Results grid (cards + first-load skeletons share the grid) -->
      <div class="discover__grid">
        <DiscoverCard
          v-for="it in items"
          :key="`${it.source}-${it.mangaId}`"
          :candidate="it"
          @inspect="emit('inspect', $event)"
          @adopt="emit('adopt', $event)"
          @open-source-link="emit('openSourceLink', $event)"
          @hover="emit('hover', $event)"
        />

        <!-- First-load skeletons -->
        <template v-if="isFirstLoad">
          <div v-for="n in skeletons" :key="`sk-${n}`" class="disc-skel">
            <div class="disc-skel__cover" />
            <div class="disc-skel__foot" />
          </div>
        </template>
      </div>

      <!-- Empty state -->
      <p v-if="isEmpty" class="discover__empty">This source returned nothing for this listing.</p>

      <!-- Loading-more spinner row -->
      <div v-if="isLoadingMore" class="discover__more-loading">
        <Spinner :size="15" tone="accent" />
        Loading more…
      </div>

      <!-- Load more -->
      <div v-if="hasMore" class="discover__more">
        <AppButton variant="mini" size="md" @click="loadMore">Load more</AppButton>
      </div>

      <!-- End of list -->
      <p v-if="isEnd" class="discover__end">— End of list —</p>
    </div>
  </div>
</template>

<style scoped>
/* QCAT-231 "fit the screen, scroll inside": `.discover` is bounded to ONE
 * viewport under the sticky 64px AppShell header (mirrors SeriesDetail's
 * `.columns` / PanelCard's `.panel` shape) and is itself a flex column —
 * `.discover__top` (controls + error banner) is fixed-size and flows
 * naturally, `.discover__scroll` takes the rest of the height and is the
 * ONE scroll container for the grid + pagination/empty/end states. This
 * replaces the old whole-page scroll, so paging through results never
 * scrolls the controls out of reach. `min-height: 0` on both the outer flex
 * container and the scrolling child is the same grid/flex overflow-trap
 * override PanelCard documents — without it the column refuses to shrink
 * below its content and the page-level scrollbar comes back. */
.discover {
  padding: 24px 30px 70px;
  background: var(--bg);
  height: calc(100dvh - 64px);
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.discover__top {
  flex: none;
}

.discover__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

/* ---- Controls ------------------------------------------------------------- */
.discover__controls {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 22px;
}

.discover__source {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.discover__source-label {
  font-size: var(--text-sm);
  color: var(--faint);
  font-weight: var(--weight-semibold);
  flex: none;
}

/* Preserve the prototype source-picker width (the native select had min-width:200px). */
.discover__select {
  min-width: 200px;
}

.discover__caption {
  margin: 0 0 0 auto;
  font-size: var(--text-sm);
  color: var(--faint);
}

@media (max-width: 900px) {
  /* Stack the controls: source picker + toggle each take a full-width row
   * instead of squeezing onto one line (the select's own min-width:200px
   * would otherwise force a horizontal scrollbar on a narrow phone). The
   * caption drops its auto-margin right-push (nothing left to push away
   * from once stacked) and wraps under the toggle. */
  .discover__controls {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .discover__source {
    width: 100%;
  }

  .discover__select {
    flex: 1;
    min-width: 0;
    width: 100%;
  }

  .discover__toggle {
    align-self: flex-start;
  }

  .discover__caption {
    margin: 0;
  }
}

/* ---- Error banner --------------------------------------------------------- */
.discover__error {
  background: var(--surface);
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-xl);
  padding: 30px;
  text-align: center;
  margin-bottom: 18px;
}

.discover__error-title {
  margin: 0 0 6px;
  color: var(--danger-text);
  font-weight: var(--weight-bold);
}

.discover__error-body {
  margin: 0 0 14px;
  font-size: var(--text-base);
  color: var(--muted);
}

.retry-btn {
  padding: 9px 16px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--text);
  font-family: var(--font-sans);
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  cursor: pointer;
}

.retry-btn:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}

/* ---- Grid ----------------------------------------------------------------- */
.discover__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(184px, 1fr));
  gap: 18px;
}

@media (max-width: 900px) {
  /* A narrower tile + tighter outer padding so a phone (390px and down to
   * ~320px) still fits 2 columns instead of collapsing to 1 — the 184px
   * desktop tile only leaves room for a single column once the 30px side
   * padding is subtracted. */
  .discover {
    padding: 16px 14px 60px;
  }

  .discover__grid {
    grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
    gap: 10px;
  }
}

/* ---- Skeleton (first load) ------------------------------------------------ */
.disc-skel {
  border-radius: var(--radius-xl);
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--border);
}

.disc-skel__cover {
  width: 100%;
  padding-bottom: 134%;
  background: var(--surface2);
  animation: disc-pulse 1.4s ease-in-out infinite;
}

.disc-skel__foot {
  height: 33px;
}

@keyframes disc-pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}

/* ---- Empty / pagination / end --------------------------------------------- */
.discover__empty {
  padding: 60px 0;
  margin: 0;
  text-align: center;
  color: var(--muted);
}

.discover__more-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 26px 0;
  color: var(--muted);
  font-size: var(--text-base);
}

.discover__more {
  display: flex;
  justify-content: center;
  margin-top: 26px;
}

.discover__end {
  padding: 26px 0;
  margin: 0;
  text-align: center;
  color: var(--faint);
  font-size: var(--text-sm);
}
</style>
