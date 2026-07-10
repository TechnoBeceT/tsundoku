<script setup lang="ts">
import IconButton from '~/components/ui/IconButton.vue'
import ProgressBar from '~/components/ui/ProgressBar.vue'

/**
 * ReaderChrome — the reader's hide-on-tap overlay chrome. Two bars pinned over
 * the strip:
 *   - TOP: a back button + the series title / current-chapter label.
 *   - BOTTOM: a reading-progress bar with a "page X / N" readout + a settings
 *     button.
 *
 * `visible` slides both bars off-screen (top up, bottom down) and disables their
 * pointer events when false, so the reader route can toggle the chrome on a
 * centre tap without the hidden bars swallowing scrolls/taps. Presentation-only:
 * it renders the passed props and emits `back` / `toggle-settings`; the route
 * owns visibility + the progress figures.
 *
 * The root carries `data-reader-chrome` so the route's tap handler can tell a
 * chrome-control click apart from a centre tap and NOT toggle on the former.
 */
defineProps<{
  /** Whether the bars are shown; false slides them out + disables their events. */
  visible: boolean
  /** The series title (top bar heading). */
  title: string
  /** The current chapter label, e.g. "Chapter 12 · Title" (top bar subheading). */
  chapterLabel: string
  /** The page readout, e.g. "8 / 34" (bottom bar). */
  pageLabel: string
  /** Overall reading progress 0–100 for the bottom progress bar. */
  percent: number
}>()

const emit = defineEmits<{
  /** The back button was activated — return to the series. */
  back: []
  /** The settings button was activated — open/close the settings sheet. */
  'toggle-settings': []
}>()
</script>

<template>
  <div
    class="chrome"
    :class="{ 'chrome--hidden': !visible }"
    data-reader-chrome
  >
    <header class="chrome__bar chrome__bar--top">
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <IconButton class="chrome__btn" :ariaLabel="'Back to series'" @click="emit('back')">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="m12 19-7-7 7-7" />
          <path d="M19 12H5" />
        </svg>
      </IconButton>
      <div class="chrome__meta">
        <p class="chrome__title">{{ title }}</p>
        <p v-if="chapterLabel" class="chrome__chapter">{{ chapterLabel }}</p>
      </div>
    </header>

    <footer class="chrome__bar chrome__bar--bottom">
      <div class="chrome__progress">
        <ProgressBar :value="percent" />
        <span class="chrome__pages">{{ pageLabel }}</span>
      </div>
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <IconButton class="chrome__btn" :ariaLabel="'Reader settings'" @click="emit('toggle-settings')">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
      </IconButton>
    </footer>
  </div>
</template>

<style scoped>
/* The overlay itself never captures pointer events — clicks fall through to the
   strip beneath for the centre-tap toggle; only the two bars are interactive. */
.chrome {
  position: absolute;
  inset: 0;
  z-index: 20;
  pointer-events: none;
}

.chrome__bar {
  position: absolute;
  left: 0;
  right: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  pointer-events: auto;
  background: color-mix(in srgb, var(--bg) 82%, transparent);
  backdrop-filter: blur(8px);
  transition: transform 0.22s ease, opacity 0.22s ease;
}

.chrome__bar--top {
  top: 0;
  border-bottom: 1px solid var(--border);
}

.chrome__bar--bottom {
  bottom: 0;
  border-top: 1px solid var(--border);
}

/* Hidden: slide each bar off its edge, fade out, and stop capturing events. */
.chrome--hidden .chrome__bar {
  opacity: 0;
  pointer-events: none;
}

.chrome--hidden .chrome__bar--top {
  transform: translateY(-100%);
}

.chrome--hidden .chrome__bar--bottom {
  transform: translateY(100%);
}

.chrome__btn {
  flex: none;
}

.chrome__meta {
  min-width: 0;
}

.chrome__title {
  margin: 0;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-md);
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.chrome__chapter {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.chrome__progress {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
  min-width: 0;
}

.chrome__pages {
  flex: none;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
</style>
