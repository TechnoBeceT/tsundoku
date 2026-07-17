<script setup lang="ts">
import { computed } from 'vue'
import SelectField from '../ui/SelectField.vue'
import SegmentedToggle from '../ui/SegmentedToggle.vue'
import AppButton from '../ui/AppButton.vue'
import Spinner from '../ui/Spinner.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
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
    <!-- Top: controls + error banner, flowing naturally above the growing grid -->
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

    <!-- Results grid (QCAT-265 GROW: the grid GROWS with content and the page
         scrolls — no letterbox, no inner-scroll). The ONE fluid primitive
         (ResponsiveGrid, QCAT-259): cards + first-load skeletons share it. -->
    <ResponsiveGrid
      class="discover__grid"
      min-tile="184px"
      gap="var(--space-xl)"
      mobile-min-tile="132px"
      mobile-gap="var(--space-sm)"
      :phone-columns="2"
    >
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
    </ResponsiveGrid>

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
</template>

<style scoped>
/* QCAT-265 GROW: the Discover browse grid is the GROW case — the document
 * scrolls and the grid grows with content. The old QCAT-231 letterbox
 * (`height: calc(100dvh - 64px)` + a `flex:1 / min-height:0 / overflow-y:auto`
 * inner-scroll region) was experience drift (§0.1): on a large screen the owner
 * was "trying to work inside a small area". Stripped — no viewport-keyed height,
 * no inner-scroll. The prototype's browse grid is a plain growing grid. Spacing
 * is on the fluid token ladder (byte-identical at the 16px anchor: 24px 30px,
 * 70px trailing). `--app-nav-bottom` (0 on desktop) clears the phone bottom-nav
 * so the last row / Load-more is never occluded. */
.discover {
  padding: var(--space-2xl) var(--space-3xl)
    calc(4.375rem + var(--app-nav-bottom)); /* 24px 30px 70px @16 */
  background: var(--bg);
}

/* ---- Controls ------------------------------------------------------------- */
.discover__controls {
  display: flex;
  align-items: center;
  gap: var(--space-base); /* 14px @16 */
  flex-wrap: wrap;
  margin-bottom: 1.375rem; /* 22px @16 — off-ladder, byte-identical rem literal */
}

.discover__source {
  display: flex;
  align-items: center;
  gap: 0.5625rem; /* 9px @16 — off-ladder, byte-identical rem literal */
  min-width: 0;
}

.discover__source-label {
  font-size: var(--text-sm);
  color: var(--faint);
  font-weight: var(--weight-semibold);
  flex: none;
}

/* Preserve the prototype source-picker width (the native select had
   min-width:200px; 12.5rem = 200px @16, now rides the fluid root). */
.discover__select {
  min-width: 12.5rem;
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
    gap: var(--space-sm); /* 10px @16 */
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
  padding: var(--space-3xl); /* 30px @16 */
  text-align: center;
  margin-bottom: var(--space-xl); /* 18px @16 */
}

.discover__error-title {
  margin: 0 0 var(--space-xs-tight); /* 6px @16 */
  color: var(--danger-text);
  font-weight: var(--weight-bold);
}

.discover__error-body {
  margin: 0 0 var(--space-base); /* 14px @16 */
  font-size: var(--text-base);
  color: var(--muted);
}

.retry-btn {
  padding: 0.5625rem var(--space-lg); /* 9px 16px @16 — 9px off-ladder literal */
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

/* ---- Grid -----------------------------------------------------------------
 * The grid is the ONE fluid primitive (ResponsiveGrid, QCAT-259) — the template
 * sets its props (desktop min-tile 184px byte-identical to the prototype's
 * `minmax(184px,1fr)` at the anchor, gap 18px; ≤900px narrows to 132px/10px;
 * ≤430px HOLDS 2 columns and grows the tiles, QCAT-263). No `grid-template`
 * lives here anymore. DiscoverCard keeps 2 phone columns (not the library's 3)
 * because its action foot carries TWO interactive controls (+Adopt / Source)
 * that need more tile width than a pure-cover SeriesCard. */

/* ---- COMPACT mobile density (QCAT-261) -------------------------------------
 * Tighten the top padding + side gutters (~half) so the phone packs content
 * densely like Komikku. `--app-nav-bottom` clears the fixed bottom-nav so the
 * last row / Load-more is never occluded. DESKTOP (≥901px) is untouched. */
@media (max-width: 900px) {
  .discover {
    padding: var(--space-lg) var(--space-base)
      calc(var(--space-lg) + var(--app-nav-bottom)); /* 16px 14px @16 + nav */
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
  height: 2.0625rem; /* 33px @16 — off-ladder, byte-identical rem literal */
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
  padding: 3.75rem 0; /* 60px @16 — off-ladder, byte-identical rem literal */
  margin: 0;
  text-align: center;
  color: var(--muted);
}

.discover__more-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-sm); /* 10px @16 */
  padding: 1.625rem 0; /* 26px @16 — off-ladder, byte-identical rem literal */
  color: var(--muted);
  font-size: var(--text-base);
}

.discover__more {
  display: flex;
  justify-content: center;
  margin-top: 1.625rem; /* 26px @16 */
}

.discover__end {
  padding: 1.625rem 0; /* 26px @16 */
  margin: 0;
  text-align: center;
  color: var(--faint);
  font-size: var(--text-sm);
}
</style>
