<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
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

    <!-- QCAT-231 "fit the screen, scroll inside": everything below the head
         (loading / empty states / the card grid) lives in ONE bounded,
         internally-scrolling region so a long sick-series report scrolls
         WITHIN itself, never as an infinite page (mirrors LibraryList's
         `.library__scroll`). -->
    <div class="health__scroll">
      <!-- Loading skeletons -->
      <div v-if="loading" class="grid">
        <Skeleton v-for="n in skeletons" :key="n" variant="card" height="180px" />
      </div>

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
      <div v-else class="grid">
        <SickSeriesCard
          v-for="s in series"
          :key="s.id"
          :series="s"
          @open-series="emit('open-series', $event)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
/* QCAT-231 "fit the screen, scroll inside": `.health` is bounded to the
 * viewport under AppShell's sticky 64px header (`shell/AppShell.vue`'s `.head`
 * — untouched here) and laid out as a flex column. `.health__head` (the intro
 * + rescan button) is flex:none — its natural, content-driven height,
 * including whatever extra height it takes when it wraps on a narrow screen —
 * and `.health__scroll` takes whatever height is left and is the ONE scroll
 * container, mirroring LibraryList's `.library`/`.library__scroll` shape.
 * Holds at every width with ZERO `@media` (QCAT-230) — a single flex column
 * needs no breakpoint override to re-bound itself when the head wraps taller. */
.health {
  padding: 24px 30px 0;
  background: var(--bg);
  height: calc(100dvh - 64px);
  display: flex;
  flex-direction: column;
}

/* ---- Head: intro + rescan -------------------------------------------------- */
.health__head {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}

.health__intro {
  max-width: 560px;
  min-width: 0;
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--muted);
  overflow-wrap: anywhere;
}

/* The inner-scroll region. 🔴 min-height: 0 is the same flex-item overflow
 * trap PanelCard.vue / LibraryList document: without it this region refuses
 * to shrink below its content (every sick-series card) and the bounded
 * `.health` above would grow instead of scrolling internally — the
 * page-level scroll QCAT-231 exists to kill would come back. The trailing
 * padding is the breathing room after the last row. */
.health__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 30px;
}

/* ---- Card grid --------------------------------------------------------------
 * `auto-fill` + a `minmax` floor is inherently responsive at every width — no
 * `@media` needed. The floor was previously 540px, which forced horizontal
 * overflow (and killed page scroll — QCAT-230) on any viewport narrower than
 * ~600px, including every phone. 300px is comfortably under a 390px phone's
 * content width (390 - 2*30 padding = 330px) so a single column always fits;
 * it scales back up to multiple columns on tablet/desktop. */
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 14px;
  align-items: start;
}
</style>
