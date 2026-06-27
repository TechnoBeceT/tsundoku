<script setup lang="ts">
import { computed } from 'vue'
import type { BrowseResult, BrowseType, DiscoverCandidate, DiscoverSource } from './discover.types'

/**
 * Discover — per-source catalog browse: a source picker + Popular/Latest toggle
 * over a cover-forward grid of manga candidates, each with a robust hover-preview
 * popup. It is the browse-driven sibling of Search; both feed the SAME Adopt flow.
 *
 * Presentation only: ALL data arrives via props and every action is emitted — no
 * fetching, routing, or stores. It references only design tokens, so it reads
 * correctly in both themes.
 *
 * Fixes the two old-Kaizoku Discover bugs:
 *  - Bug 1 (dead navigation): a primary card click emits `inspect`/`adopt` to open
 *    the in-app Adopt flow — it NEVER routes to a series-detail page (that route
 *    only exists for adopted series). "View on source ↗" is a real external `<a>`
 *    (new tab, `rel="noopener noreferrer"`), never an internal route.
 *  - Bug 2 (broken hover popup): the preview is a direct sibling of the card's
 *    overflow-clipped inner box (so it is never clipped), is `position:absolute`
 *    (zero layout shift), uses `pointer-events:none` (no flicker as the cursor
 *    crosses it), and lifts its card with `z-index` on hover (never covered).
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

// Source-picker option labels: "Name · LANG".
const sourceLabel = (s: DiscoverSource): string => `${s.name} · ${s.lang.toUpperCase()}`

// The big faint placeholder letter behind a cover (first char, uppercased).
const initial = (title: string): string => (title.trim()[0] ?? '?').toUpperCase()

// The popup's "source · LANG" line.
const candidateSource = (c: DiscoverCandidate): string => `${c.sourceName} · ${c.lang.toUpperCase()}`

// A handful of skeleton placeholders for the first-load grid.
const skeletons = Array.from({ length: 12 }, (_, i) => i)

const onSource = (e: Event): void => emit('setSource', (e.target as HTMLSelectElement).value)
const loadMore = (): void => emit('page', props.result.page + 1)

// "View on source" notifies the parent but does NOT preventDefault — the real
// `<a target="_blank">` still opens the source in a new tab (Bug 1 fix). Stop
// propagation so it doesn't also trigger the card's inspect.
const onSourceLink = (c: DiscoverCandidate, e: Event): void => {
  e.stopPropagation()
  emit('openSourceLink', c)
}
</script>

<template>
  <div class="discover">
    <!-- Controls: source picker + Popular/Latest toggle + caption -->
    <div class="discover__controls">
      <div class="discover__source">
        <span class="discover__source-label">Source</span>
        <select class="select" :value="activeSource" @change="onSource">
          <option v-for="s in sources" :key="s.id" :value="s.id">{{ sourceLabel(s) }}</option>
        </select>
      </div>

      <div class="seg">
        <button
          type="button"
          class="seg__btn"
          :class="{ 'seg__btn--active': activeType === 'popular' }"
          @click="emit('setType', 'popular')"
        >
          Popular
        </button>
        <button
          type="button"
          class="seg__btn"
          :class="{ 'seg__btn--active': activeType === 'latest' }"
          @click="emit('setType', 'latest')"
        >
          Latest
        </button>
      </div>

      <p class="discover__caption">Browse a source &amp; adopt — covers are the focus</p>
    </div>

    <!-- Error banner (the active source failed) -->
    <div v-if="error" class="discover__error">
      <p class="discover__error-title">Couldn't reach this source</p>
      <p class="discover__error-body">The source returned an error. It may be temporarily down.</p>
      <button type="button" class="retry-btn" @click="emit('retry')">Retry</button>
    </div>

    <!-- Results grid (cards + first-load skeletons share the grid) -->
    <div class="discover__grid">
      <div v-for="it in items" :key="`${it.source}-${it.mangaId}`" class="disc-card">
        <!-- Inner box is overflow-clipped; the popup is its SIBLING (never clipped) -->
        <div class="disc-card__box">
          <button
            type="button"
            class="disc-card__cover"
            :aria-label="`Inspect ${it.title}`"
            @click="emit('inspect', it)"
          >
            <div class="disc-card__placeholder">
              <span class="disc-card__initial">{{ initial(it.title) }}</span>
            </div>
            <img
              v-if="it.thumbnailUrl"
              class="disc-card__img"
              :src="it.thumbnailUrl"
              :alt="`${it.title} cover`"
              loading="lazy"
            >
            <div class="disc-card__scrim" />
            <span v-if="it.inLibrary" class="in-lib">
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M20 6L9 17l-5-5" />
              </svg>
              IN LIBRARY
            </span>
            <div class="disc-card__title-wrap">
              <div class="disc-card__title">{{ it.title }}</div>
            </div>
          </button>

          <div class="disc-card__foot">
            <button type="button" class="adopt-btn" @click="emit('adopt', it)">+ Adopt</button>
            <a
              class="source-link"
              :href="it.url"
              target="_blank"
              rel="noopener noreferrer"
              @click="onSourceLink(it, $event)"
            >
              Source
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M7 17L17 7M17 7H8M17 7v9" />
              </svg>
            </a>
          </div>
        </div>

        <!-- Hover-preview popup (Bug 2 fix — see component doc comment) -->
        <div class="disc-pop">
          <div class="disc-pop__cover">
            <div class="disc-pop__placeholder">
              <span class="disc-pop__initial">{{ initial(it.title) }}</span>
            </div>
            <img v-if="it.thumbnailUrl" class="disc-pop__img" :src="it.thumbnailUrl" :alt="`${it.title} cover`">
            <div class="disc-pop__scrim" />
            <div class="disc-pop__title-wrap">
              <div class="disc-pop__title">{{ it.title }}</div>
            </div>
          </div>
          <div class="disc-pop__body">
            <div class="disc-pop__source">
              {{ candidateSource(it) }}<template v-if="it.inLibrary"> · <span class="disc-pop__in-lib">In library</span></template>
            </div>
            <p class="disc-pop__desc">{{ it.description || 'No description available for this title.' }}</p>
            <div v-if="it.genres && it.genres.length" class="disc-pop__genres">
              <span v-for="g in it.genres" :key="g" class="genre">{{ g }}</span>
            </div>
          </div>
        </div>
      </div>

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
      <span class="spinner" aria-hidden="true" />
      Loading more…
    </div>

    <!-- Load more -->
    <div v-if="hasMore" class="discover__more">
      <button type="button" class="more-btn" @click="loadMore">Load more</button>
    </div>

    <!-- End of list -->
    <p v-if="isEnd" class="discover__end">— End of list —</p>
  </div>
</template>

<style scoped>
/* Discover-specific cover tokens. The canonical global home is index.css
 * (`@import './tokens/discover.css';`); imported here too so the component
 * ships able to render on its own. The :root defs are idempotent. */
@import '../../assets/css/tokens/discover.css';

.discover {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
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
}

.discover__source-label {
  font-size: var(--text-sm);
  color: var(--faint);
  font-weight: var(--weight-semibold);
}

.select {
  min-width: 200px;
  padding: 9px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  outline: none;
}

.select:focus-visible {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.seg {
  display: flex;
  gap: 4px;
  padding: 4px;
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
}

.seg__btn {
  padding: 8px 16px;
  border-radius: 9px;
  border: none;
  background: transparent;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.seg__btn:hover {
  color: var(--text);
}

.seg__btn--active {
  background: var(--accent);
  color: var(--cover-text);
}

.discover__caption {
  margin: 0 0 0 auto;
  font-size: var(--text-sm);
  color: var(--faint);
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

/* ---- Card ----------------------------------------------------------------- */
.disc-card {
  position: relative;
}

/* Lift the whole card above its neighbours so the popup is never covered. */
.disc-card:hover {
  z-index: 40;
}

.disc-card__box {
  position: relative;
  display: flex;
  flex-direction: column;
  border-radius: var(--radius-xl);
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--border);
  transition: transform 0.15s, border-color 0.15s;
}

.disc-card:hover .disc-card__box {
  transform: translateY(-4px);
  border-color: var(--border2);
}

.disc-card__cover {
  position: relative;
  display: block;
  width: 100%;
  padding: 0;
  padding-bottom: 134%;
  border: none;
  background: none;
  cursor: pointer;
  overflow: hidden;
}

.disc-card__cover:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}

.disc-card__placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
}

.disc-card__initial {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: 58px;
  color: var(--disc-initial);
}

.disc-card__img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.disc-card__scrim {
  position: absolute;
  inset: 0;
  background: var(--cover-scrim);
}

.in-lib {
  position: absolute;
  top: 8px;
  left: 8px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border-radius: var(--radius-pill);
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  color: var(--cover-done);
  background: var(--cover-done-bg);
  backdrop-filter: blur(4px);
}

.disc-card__title-wrap {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 10px;
}

.disc-card__title {
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--cover-text);
  line-height: 1.22;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.disc-card__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 11px;
  border-top: 1px solid var(--border);
}

.adopt-btn {
  padding: 0;
  border: none;
  background: none;
  color: var(--accentBright);
  font-family: var(--font-sans);
  font-size: 11.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.source-link {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--text-xs);
  color: var(--faint);
  text-decoration: none;
  transition: color 0.15s;
}

.source-link:hover {
  color: var(--text);
}

/* ---- Hover-preview popup -------------------------------------------------- */
.disc-pop {
  position: absolute;
  top: -6px;
  left: 50%;
  margin-left: -152px;
  width: 304px;
  background: var(--surface);
  border: 1px solid var(--border2);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  box-shadow: var(--shadow);
  /* Hidden until card hover; pointer-events:none kills cursor flicker. */
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
  transition: opacity 0.16s ease, visibility 0.16s ease;
}

.disc-card:hover .disc-pop {
  opacity: 1;
  visibility: visible;
}

.disc-pop__cover {
  position: relative;
  width: 100%;
  height: 172px;
  overflow: hidden;
}

.disc-pop__placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
}

.disc-pop__initial {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: 70px;
  color: var(--disc-initial-lg);
}

.disc-pop__img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.disc-pop__scrim {
  position: absolute;
  inset: 0;
  background: var(--disc-pop-scrim);
}

.disc-pop__title-wrap {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 13px;
}

.disc-pop__title {
  font-family: var(--font-display);
  font-weight: var(--weight-extrabold);
  font-size: var(--text-lg);
  color: var(--cover-text);
  line-height: 1.2;
}

.disc-pop__body {
  padding: 12px 14px;
}

.disc-pop__source {
  font-size: var(--text-xs);
  color: var(--faint);
  margin-bottom: 9px;
}

.disc-pop__in-lib {
  color: var(--cover-done);
  font-weight: var(--weight-bold);
}

.disc-pop__desc {
  margin: 0 0 11px;
  font-size: var(--text-sm);
  color: var(--muted);
  line-height: 1.55;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.disc-pop__genres {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.genre {
  font-size: 10px;
  font-weight: var(--weight-bold);
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
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

.spinner {
  width: 15px;
  height: 15px;
  border: 2px solid var(--accent);
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: disc-spin 0.8s linear infinite;
}

@keyframes disc-spin {
  to {
    transform: rotate(360deg);
  }
}

.discover__more {
  display: flex;
  justify-content: center;
  margin-top: 26px;
}

.more-btn {
  padding: 11px 22px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.more-btn:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}

.discover__end {
  padding: 26px 0;
  margin: 0;
  text-align: center;
  color: var(--faint);
  font-size: var(--text-sm);
}
</style>
