<script setup lang="ts">
import { ref, watch } from 'vue'

/**
 * ReaderPage — one page of the long-strip reader: a plain lazy `<img>` that loads
 * the page bytes directly from a same-origin URL. Mirrors `CoverImage` (QCAT-020:
 * cookie auth rides `<img src>`, so there is NO fetch/objectURL machinery — the
 * browser lazy-loads and evicts page images natively).
 *
 * A LOADED page renders at its NATURAL height — no fixed aspect is imposed, so a
 * tall manhwa/webtoon strip and a standard manga page both render true-to-size
 * with no over/under-reserve mismatch shoving the seam. Only the PENDING/FAILED
 * state reserves a neutral min-height so the strip has some layout before the
 * bytes arrive; native CSS scroll-anchoring (`overflow-anchor`, on by default)
 * keeps the read position steady when an above-viewport page finishes loading.
 * On a load error it shows the "page unavailable" placeholder (CoverImage's empty
 * tile); the ReaderStrip uses the emitted `error` to apply the pageCount tail-404
 * tolerance.
 *
 * NOTE: the page-gap + column fit are driven at the strip level by the reader
 * settings (CSS custom properties). The pending/failed reserve height stays a
 * fixed default (`--reader-page-reserve`, 60vh) — it is not a v1 user setting.
 */
const props = withDefaults(defineProps<{
  /** Page image URL (same-origin). Empty shows the loading placeholder. */
  src?: string
  /** Alt text for the page image. */
  alt?: string
  /** Distance (in pages) from the reader's live centred position, supplied by
   *  `ReaderStrip`. Defaults large (effectively "far") so callers that don't
   *  pass it behave as before. A later slice uses this to bias eager
   *  preloading toward pages nearest the reader's position — not yet consumed
   *  here. */
  distanceFromCentre?: number
}>(), {
  src: '',
  alt: 'Page',
  distanceFromCentre: Infinity,
})

const emit = defineEmits<{
  /** The page image finished loading. */
  load: []
  /** The page image failed to load (missing/out-of-range page). */
  error: []
}>()

// Per-src load state: `loaded` drops the reserved aspect (natural height rules);
// `failed` swaps in the unavailable placeholder. Both reset when `src` changes so
// a re-pointed page (e.g. an upgrade replacing the bytes) re-loads cleanly.
const loaded = ref(false)
const failed = ref(false)

watch(() => props.src, () => {
  loaded.value = false
  failed.value = false
})

function onLoad(): void {
  loaded.value = true
  emit('load')
}

function onError(): void {
  failed.value = true
  emit('error')
}
</script>

<template>
  <div
    class="page"
    :class="{ 'page--reserved': !loaded || failed }"
  >
    <img
      v-if="src && !failed"
      class="page__img"
      :src="src"
      :alt="alt"
      loading="lazy"
      @load="onLoad"
      @error="onError"
    >
    <div v-if="failed" class="page__placeholder">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <path d="m3 15 5-5 4 4" />
        <path d="M14 14l2-2 5 5" />
        <path d="m2 2 20 20" />
      </svg>
      <span class="page__msg">Page unavailable</span>
    </div>
  </div>
</template>

<style scoped>
.page {
  position: relative;
  width: 100%;
  background: var(--cover-placeholder);
}

/* Full-bleed page image; inter-page spacing (when enabled) is applied by the
   strip's `.strip__chapter` gap, not here. */
.page__img {
  display: block;
  width: 100%;
  height: auto;
}

/* Pending/failed: reserve a NEUTRAL min-height so the strip has some layout
   before the bytes arrive — deliberately NOT a fixed aspect (a manhwa strip and a
   manga page have wildly different heights; forcing one wrong aspect on the
   loaded image is what shoves the seam). Once loaded the class drops and the
   natural image height rules. Slice 4 makes --reader-page-reserve configurable. */
.page--reserved {
  min-height: var(--reader-page-reserve, 60vh);
}

.page__placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--faint);
  background: var(--cover-placeholder);
}

.page__msg {
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
}
</style>
