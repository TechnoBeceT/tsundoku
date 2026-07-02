<script setup lang="ts">
import Chip from '../ui/Chip.vue'
import type { DiscoverCandidate } from '../screens/discover.types'

/**
 * DiscoverHoverPreview — the rich hover-preview popup shown when a DiscoverCard is
 * hovered: a larger cover header (initial-letter placeholder or image + scrim +
 * title), then a body with the "source · LANG · In library" line, an optional
 * "by {author} · art by {artist}" credit line (M4), a clamped description, and
 * the candidate's genres as <Chip>s.
 *
 * BUG-2 FIX — this is a deliberate Kaizoku bug-fix and its structure MUST be
 * preserved by the owning DiscoverCard: the popup is a direct SIBLING of the
 * card's overflow-clipped inner box (so it is never clipped), is
 * `position:absolute` (zero layout shift), and uses `pointer-events:none` (no
 * flicker as the cursor crosses it). It is hidden by default and revealed by the
 * card's `:hover` (the card also lifts its own `z-index` so the popup is never
 * covered). It carries NO own hover state — visibility is driven entirely by the
 * parent card, except the story-only `visible` escape hatch below.
 *
 * Presentation only: everything arrives via the `candidate` prop; no emits.
 */
withDefaults(defineProps<{
  /** The candidate to preview (cover, title, source, description, genres). */
  candidate: DiscoverCandidate
  /** Force the popup visible (stories / a future click-to-pin); the card leaves
   *  this false and reveals via CSS `:hover` instead. */
  visible?: boolean
}>(), {
  visible: false,
})

// The big faint placeholder letter behind the cover (first char, uppercased).
const initial = (title: string): string => (title.trim()[0] ?? '?').toUpperCase()

// The popup's "source · LANG" line.
const candidateSource = (c: DiscoverCandidate): string => `${c.sourceName} · ${c.lang.toUpperCase()}`

// The "by {author} · art by {artist}" credit line. Artist is only appended
// when it's set AND differs from author — a single-credit work (very common;
// many sources set author === artist, or omit artist) shows just "by {author}"
// instead of a redundant repeat.
const creditLine = (c: DiscoverCandidate): string => {
  if (!c.author) return ''
  return c.artist && c.artist !== c.author
    ? `by ${c.author} · art by ${c.artist}`
    : `by ${c.author}`
}
</script>

<template>
  <div class="disc-pop" :class="{ 'disc-pop--visible': visible }">
    <div class="disc-pop__cover">
      <div class="disc-pop__placeholder">
        <span class="disc-pop__initial">{{ initial(candidate.title) }}</span>
      </div>
      <img v-if="candidate.thumbnailUrl" class="disc-pop__img" :src="candidate.thumbnailUrl" :alt="`${candidate.title} cover`">
      <div class="disc-pop__scrim" />
      <div class="disc-pop__title-wrap">
        <div class="disc-pop__title">{{ candidate.title }}</div>
      </div>
    </div>
    <div class="disc-pop__body">
      <div class="disc-pop__source">
        {{ candidateSource(candidate) }}<template v-if="candidate.inLibrary"> · <span class="disc-pop__in-lib">In library</span></template>
      </div>
      <p v-if="creditLine(candidate)" class="disc-pop__credit">{{ creditLine(candidate) }}</p>
      <p class="disc-pop__desc">{{ candidate.description || 'No description available for this title.' }}</p>
      <div v-if="candidate.genres && candidate.genres.length" class="disc-pop__genres">
        <Chip v-for="g in candidate.genres" :key="g" variant="neutral">{{ g }}</Chip>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Discover-specific cover tokens (initial-letter glyph + popup scrim). The
 * canonical global home is index.css; imported here too so the component ships
 * able to render on its own. The :root defs are idempotent. */
@import '../../assets/css/tokens/discover.css';

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

/* Story-only / click-to-pin escape hatch — the card reveals via :hover instead. */
.disc-pop--visible {
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

.disc-pop__credit {
  margin: 0 0 8px;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--muted);
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
</style>
