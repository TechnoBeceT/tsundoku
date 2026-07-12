<script setup lang="ts">
import { ref, computed } from 'vue'
import IconButton from '~/components/ui/IconButton.vue'
import { showTicks, pageToFraction, fractionToPage } from './ReaderPageSlider.logic'

/**
 * ReaderPageSlider — the reader's chapter-local page control: a draggable page
 * slider flanked by prev/next CHAPTER buttons. Replaces the old series-wide
 * `<ProgressBar>` + "page X / N" readout, which mixed two different scales in
 * one visual unit (a whole chapter moved the bar ~0.3% on a 300-chapter series
 * while the label beside it read chapter-local page counts). PRESENTATION ONLY
 * — no `useReader` import; the route owns all reader state and reacts to the
 * emits below.
 *
 * Layout: `[|◀ prev]  [ page ●━━━━━ lastPage ]  [▶| next]`. The prev/next
 * buttons navigate CHAPTERS (not pages) and are always rendered, `:disabled`
 * rather than hidden — a control that moves position is worse than one that's
 * inert (`hasPrev`/`hasNext` gate them).
 *
 * The track is both click- and drag-seekable via Pointer Events with pointer
 * capture: capture is taken on the track element itself on `pointerdown`, so a
 * drag that leaves the track's bounding box still delivers `pointermove`/
 * `pointerup` to it (the browser redirects captured pointer events to the
 * capturing element regardless of where the pointer physically is).
 *
 * `page` is the SOURCE OF TRUTH the thumb renders from — this component holds
 * no internal drag-position state. The route is expected to update `page`
 * OPTIMISTICALLY the moment it handles a `seek` emit (not wait for the reader
 * strip's scroll to settle), so the thumb tracks the pointer with no lag; see
 * the route's `onSeek`/`onCentered` guard for how it stops the strip's
 * resulting scroll echo from fighting that optimistic value.
 */
const props = defineProps<{
  /** 0-based current page within the chapter — the thumb's position source. */
  page: number
  /** The chapter's TRIMMED visible page count (never the declared `pageCount`,
   *  which may exceed the CBZ's real image count) — the slider's denominator. */
  visiblePages: number
  /** Whether a previous chapter exists; false disables (never hides) prev. */
  hasPrev: boolean
  /** Whether a next chapter exists; false disables (never hides) next. */
  hasNext: boolean
}>()

const emit = defineEmits<{
  /** The track was clicked or dragged to a new 0-based page. */
  seek: [page: number]
  /** The prev-chapter button was activated. */
  prev: []
  /** The next-chapter button was activated. */
  next: []
}>()

const trackEl = ref<HTMLElement | null>(null)
const dragging = ref(false)

/** The thumb/fill position as a 0..1 track fraction. */
const fraction = computed(() => pageToFraction(props.page, props.visiblePages))

/** One dot per page, only below the smear threshold (see `showTicks`'s doc comment). */
const tickPages = computed(() =>
  showTicks(props.visiblePages) ? Array.from({ length: props.visiblePages }, (_, i) => i) : [])

/** Converts a pointer's viewport X into a 0-based page and emits `seek`. */
function seekToClientX(clientX: number): void {
  const el = trackEl.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  if (rect.width <= 0) return
  const trackFraction = (clientX - rect.left) / rect.width
  emit('seek', fractionToPage(trackFraction, props.visiblePages))
}

function onPointerDown(event: PointerEvent): void {
  dragging.value = true
  trackEl.value?.setPointerCapture(event.pointerId)
  seekToClientX(event.clientX)
}

function onPointerMove(event: PointerEvent): void {
  if (!dragging.value) return
  seekToClientX(event.clientX)
}

function onPointerUp(event: PointerEvent): void {
  if (!dragging.value) return
  dragging.value = false
  trackEl.value?.releasePointerCapture(event.pointerId)
}

/** Arrow/Home/End keyboard control — the track carries `role="slider"`, so it
 *  needs a keyboard path independent of pointer dragging (a11y baseline). */
function onKeydown(event: KeyboardEvent): void {
  switch (event.key) {
    case 'ArrowLeft':
    case 'ArrowDown':
      emit('seek', Math.max(0, props.page - 1))
      break
    case 'ArrowRight':
    case 'ArrowUp':
      emit('seek', Math.min(Math.max(props.visiblePages - 1, 0), props.page + 1))
      break
    case 'Home':
      emit('seek', 0)
      break
    case 'End':
      emit('seek', Math.max(props.visiblePages - 1, 0))
      break
    default:
      return
  }
  event.preventDefault()
}
</script>

<template>
  <div class="slider">
    <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
    <IconButton class="slider__btn" size="sm" :ariaLabel="'Previous chapter'" :disabled="!hasPrev" @click="emit('prev')">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M19 20 9 12l10-8v16Z" />
        <line x1="5" y1="4" x2="5" y2="20" />
      </svg>
    </IconButton>

    <div class="slider__track-wrap">
      <span class="slider__num">{{ page + 1 }}</span>
      <div
        ref="trackEl"
        class="slider__track"
        role="slider"
        tabindex="0"
        aria-label="Chapter page"
        :aria-valuemin="1"
        :aria-valuemax="Math.max(visiblePages, 1)"
        :aria-valuenow="page + 1"
        @pointerdown="onPointerDown"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
        @pointercancel="onPointerUp"
        @keydown="onKeydown"
      >
        <div class="slider__rail">
          <div class="slider__fill" :style="{ width: `${fraction * 100}%` }" />
          <span
            v-for="p in tickPages"
            :key="p"
            class="slider__tick"
            :style="{ left: `${pageToFraction(p, visiblePages) * 100}%` }"
          />
          <div class="slider__thumb" :style="{ left: `${fraction * 100}%` }" />
        </div>
      </div>
      <span class="slider__num">{{ visiblePages }}</span>
    </div>

    <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
    <IconButton class="slider__btn" size="sm" :ariaLabel="'Next chapter'" :disabled="!hasNext" @click="emit('next')">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M5 4l10 8-10 8V4Z" />
        <line x1="19" y1="4" x2="19" y2="20" />
      </svg>
    </IconButton>
  </div>
</template>

<style scoped>
.slider {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.slider__btn {
  flex: none;
}

.slider__track-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.slider__num {
  flex: none;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  min-width: 1.5em;
  text-align: center;
}

/* Taller than the visual rail — a comfortable touch/pointer target that still
   renders a slim 5px rail (below). */
.slider__track {
  position: relative;
  flex: 1;
  min-width: 0;
  height: 20px;
  display: flex;
  align-items: center;
  cursor: pointer;
  /* Pointer Events drive the drag entirely — stop the browser's own touch
     panning/scrolling from fighting it on touch devices. */
  touch-action: none;
}

.slider__track:focus-visible {
  outline: none;
}

.slider__track:focus-visible .slider__thumb {
  box-shadow: var(--ring-focus);
}

.slider__rail {
  position: relative;
  width: 100%;
  height: 5px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
}

.slider__fill {
  position: absolute;
  inset: 0 auto 0 0;
  height: 100%;
  border-radius: var(--radius-pill);
  background: var(--accent);
}

.slider__tick {
  position: absolute;
  top: 50%;
  width: 2px;
  height: 2px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--bg) 60%, transparent);
  transform: translate(-50%, -50%);
  pointer-events: none;
}

.slider__thumb {
  position: absolute;
  top: 50%;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--accent);
  border: 2px solid var(--bg);
  transform: translate(-50%, -50%);
  pointer-events: none;
  transition: box-shadow 0.15s;
}
</style>
