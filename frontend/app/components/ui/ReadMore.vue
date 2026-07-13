<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

/**
 * ReadMore — a block of body text that clamps to `lines` and reveals a
 * "Read more" / "Show less" toggle ONLY when the text actually overflows that
 * clamp. A short synopsis therefore shows no toggle at all; a long one collapses
 * to `lines` and expands in place.
 *
 * Overflow is MEASURED, not guessed: while collapsed we compare the paragraph's
 * scrollHeight to its clientHeight, and a ResizeObserver re-measures on layout
 * changes (a narrower column may make previously-fitting text overflow). The
 * toggle is a quiet accent text button; empty `text` renders nothing.
 *
 *   - `text`  (required): the body copy to show.
 *   - `lines` (default 4): the collapsed line-clamp.
 */
const props = withDefaults(defineProps<{
  /** The body copy to render (empty → the component renders nothing). */
  text: string
  /** Collapsed line-clamp count. */
  lines?: number
}>(), {
  lines: 4,
})

const expanded = ref(false)
const overflowing = ref(false)
const bodyEl = ref<HTMLElement | null>(null)
let observer: ResizeObserver | null = null

// Measure overflow only while collapsed — expanded, scrollHeight == clientHeight
// so it would read as "not overflowing" and hide the "Show less" toggle. Keeping
// the last collapsed measurement means the toggle stays put once revealed.
const measure = (): void => {
  const el = bodyEl.value
  if (!el || expanded.value) return
  overflowing.value = el.scrollHeight - el.clientHeight > 1
}

onMounted(() => {
  measure()
  if (typeof ResizeObserver !== 'undefined' && bodyEl.value) {
    observer = new ResizeObserver(() => measure())
    observer.observe(bodyEl.value)
  }
})

onBeforeUnmount(() => observer?.disconnect())

// A changed body (new series) resets the toggle and re-measures next tick.
watch(() => props.text, () => {
  expanded.value = false
  requestAnimationFrame(measure)
})

// The clamp only applies while collapsed.
const clampStyle = computed(() => (expanded.value ? {} : { '--read-more-lines': String(props.lines) }))
</script>

<template>
  <div v-if="text" class="read-more">
    <p
      ref="bodyEl"
      class="read-more__body"
      :class="{ 'read-more__body--clamped': !expanded }"
      :style="clampStyle"
    >{{ text }}</p>
    <button
      v-if="overflowing"
      type="button"
      class="read-more__toggle"
      @click="expanded = !expanded"
    >{{ expanded ? 'Show less' : 'Read more' }}</button>
  </div>
</template>

<style scoped>
.read-more__body {
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.6;
  color: var(--muted);
}

.read-more__body--clamped {
  display: -webkit-box;
  -webkit-line-clamp: var(--read-more-lines, 4);
  line-clamp: var(--read-more-lines, 4);
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.read-more__toggle {
  margin-top: 6px;
  padding: 0;
  border: none;
  background: none;
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--accentBright);
  cursor: pointer;
}

.read-more__toggle:hover {
  color: var(--accent);
}

.read-more__toggle:focus-visible {
  outline: none;
  border-radius: var(--radius-xs);
  box-shadow: var(--ring-focus);
}
</style>
