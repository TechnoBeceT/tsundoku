<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
import SickSeriesCard from '../health/SickSeriesCard.vue'
import type { SeriesHealth } from './libraryHealth.types'

/**
 * LibraryHealth — the "what needs attention" screen. Renders ONLY the sick
 * series the backend returns (those with ≥1 stale/erroring/unavailable source;
 * completed series are healthy and never appear) as a grid of SickSeriesCards.
 *
 * Presentation only: every series arrives via props and both actions are
 * emitted — no fetching, routing, or stores. An empty `series` array is the
 * all-clear EmptyState. `loading` shows skeletons; `refreshing` puts the rescan
 * button in-flight (§16: every action shows loading/success/error — success +
 * error land as fresh props from the parent's refetch). Token-only colours →
 * renders correctly in both themes.
 */
const props = withDefaults(defineProps<{
  /** The sick series to display; empty → all-clear state. */
  series: SeriesHealth[]
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
  /** When true, the rescan action is in flight (spinner + disabled). */
  refreshing?: boolean
}>(), {
  loading: false,
  refreshing: false,
})

const emit = defineEmits<{
  /** A series card was clicked — open that series' detail view. */
  'open-series': [seriesId: string]
  /** Rescan health was clicked — the parent refetches `GET /api/health`. */
  'refresh': []
}>()

// No sick series (and not loading) → the all-clear empty state.
const isEmpty = computed(() => !props.loading && props.series.length === 0)

const skeletons = Array.from({ length: 3 }, (_, i) => i)
</script>

<template>
  <div class="health">
    <!-- Intro + rescan action -->
    <div class="health__head">
      <p class="health__intro">
        Series with at least one stale, erroring, or unavailable source. Completed series are treated as healthy and excluded.
      </p>
      <AppButton variant="mini" :loading="refreshing" @click="emit('refresh')">
        <template #icon>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-2.6-6.4" />
            <path d="M21 3v6h-6" />
          </svg>
        </template>
        {{ refreshing ? 'Rescanning…' : 'Rescan health' }}
      </AppButton>
    </div>

    <!-- Loading skeletons -->
    <ResponsiveGrid
      v-if="loading"
      class="health__grid"
      min-tile="300px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <Skeleton v-for="n in skeletons" :key="n" variant="card" height="11.25rem" />
    </ResponsiveGrid>

    <!-- All-clear empty state -->
    <EmptyState
      v-else-if="isEmpty"
      title="All clear"
      sub="Every source is healthy. Nothing needs your attention."
      icon-tone="sd-hl-ok-dot"
    >
      <template #icon>
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 6L9 17l-5-5" />
        </svg>
      </template>
    </EmptyState>

    <!-- Sick-series cards -->
    <ResponsiveGrid
      v-else
      class="health__grid"
      min-tile="300px"
      gap="var(--space-base)"
      :phone-columns="1"
    >
      <SickSeriesCard
        v-for="s in series"
        :key="s.id"
        :series="s"
        @open-series="emit('open-series', $event)"
      />
    </ResponsiveGrid>
  </div>
</template>

<style scoped>
/* QCAT-265 GROW: the Library Health report is the GROW case — the document
 * scrolls and the grid grows with content. The old QCAT-231 letterbox (`height:
 * calc(100dvh - 64px)` + a `flex:1 / min-height:0 / overflow-y:auto`
 * inner-scroll region) was experience drift (§0.1): on a large screen the owner
 * was working inside a small letterboxed area. Stripped — no viewport-keyed
 * height, no inner-scroll (mirrors LibraryList / Categories). Spacing is on the
 * fluid token ladder (byte-identical at the 16px desktop anchor: 24px 30px
 * sides, 30px trailing — the old page's 0 bottom + the scroll region's 30px
 * bottom, now one padding on the page). `--app-nav-bottom` (0 on desktop) clears
 * the phone bottom-nav so the last row is never occluded. */
.health {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

/* ---- Head: intro + rescan -------------------------------------------------- */
.health__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-lg); /* 16px @16 */
  flex-wrap: wrap;
  margin-bottom: var(--space-2xl-tight); /* 20px @16 */
}

.health__intro {
  max-width: 35rem; /* 560px @16 — byte-identical rem literal */
  min-width: 0;
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
  overflow-wrap: anywhere;
}

/* ---- Card grid -------------------------------------------------------------
 * The grid is the ONE fluid primitive (ResponsiveGrid, QCAT-259): `auto-fill`
 * min-tile 300px on desktop/tablet (byte-identical to the old
 * `minmax(300px,1fr)` at the anchor, gap 14px), and a HELD 1 column growing the
 * card with the phone's width on the phone band (≤430px, QCAT-263) — the card is
 * a WIDE info surface, so 1 per row on a phone matches its natural width. The
 * template + gap live in ResponsiveGrid; this screen only sets the props.
 *
 * `align-items: start` is re-applied here because ResponsiveGrid's base rule
 * leaves the grid's default `stretch`, and sick-series cards have VARIABLE
 * heights (1 vs N unhealthy source rows) — without `start` a row would stretch
 * every card to the tallest one, changing the desktop rendering. This restores
 * the old `.grid { align-items: start }` byte-for-byte. */
.health__grid {
  align-items: start;
}

/* ---- COMPACT mobile density (QCAT-261) -------------------------------------
 * Tighten the top padding + side gutters (~half) so the phone packs densely
 * like Komikku. The grid's own phone behaviour lives in ResponsiveGrid.
 * DESKTOP (≥901px) is untouched — this block only fires ≤900px. */
@media (max-width: 900px) {
  .health {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
