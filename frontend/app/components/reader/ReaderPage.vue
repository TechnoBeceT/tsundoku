<script setup lang="ts">
import { ref, watch } from 'vue'

/**
 * ReaderPage — one page of the long-strip reader: a plain lazy `<img>` that loads
 * the page bytes directly from a same-origin URL. Mirrors `CoverImage` (QCAT-020:
 * cookie auth rides `<img src>`, so there is NO fetch/objectURL machinery — the
 * browser lazy-loads and evicts page images natively).
 *
 * While the image is in flight the box reserves height via `aspect` so the strip
 * does not jump as pages stream in. On a load error it shows the "page unavailable"
 * placeholder (the same tile treatment as CoverImage's empty state); the ReaderStrip
 * uses the emitted `error` to apply the pageCount tail-404 tolerance.
 *
 * NOTE (Slice 4): `aspect`/fit/gaps are hardcoded sane defaults here; the reader
 * settings slice will drive them from CSS vars.
 */
const props = withDefaults(defineProps<{
  /** Page image URL (same-origin). Empty shows the loading placeholder. */
  src?: string
  /** Alt text for the page image. */
  alt?: string
  /** Reserved CSS `aspect-ratio` for the box before the image loads. */
  aspect?: string
}>(), {
  src: '',
  alt: 'Page',
  aspect: '0.7',
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
    :style="{ aspectRatio: !loaded || failed ? aspect : undefined }"
  >
    <img
      v-if="src && !failed"
      class="page__img"
      :class="{ 'page__img--loaded': loaded }"
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

/* Full-bleed strip: adjacent pages sit flush. Slice 4 will make the gap configurable. */
.page__img {
  display: block;
  width: 100%;
  height: auto;
}

/* Reserved (pending/failed) box fills the aspect-boxed height so the strip has
   layout before the image streams in. */
.page--reserved {
  overflow: hidden;
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
