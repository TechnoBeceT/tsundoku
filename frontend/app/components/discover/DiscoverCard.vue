<script setup lang="ts">
import { computed } from 'vue'
import Tag from '../ui/Tag.vue'
import DiscoverHoverPreview from './DiscoverHoverPreview.vue'
import { safeHttpUrl } from '../../utils/safeUrl'
import type { DiscoverCandidate } from '../screens/discover.types'

/**
 * DiscoverCard — one cover-forward browse card in the Discover grid: a clickable
 * cover (initial-letter placeholder or image + scrim + an "IN LIBRARY" <Tag> +
 * the title), and a foot with a "+ Adopt" action and a "View on source ↗"
 * external link. It OWNS its hover-preview popup (<DiscoverHoverPreview>) and
 * renders it as the deliberate sibling of the overflow-clipped inner box.
 *
 * BUG-2 STRUCTURE (preserved verbatim): the popup is a SIBLING of `.disc-card__box`
 * (never clipped), the card is `position:relative` (the popup's anchor), and the
 * card lifts its `z-index` on hover so the popup is never covered. The reveal is
 * pure CSS (`.disc-card:hover .disc-pop`) — no JS hover state.
 *
 * BUG-1 (dead navigation) fix: a primary cover click emits `inspect` (open the
 * in-app Adopt/Inspect flow — NEVER a series-detail route); "+ Adopt" emits
 * `adopt`; "View on source ↗" is a real external `<a target="_blank">` that still
 * opens while also emitting `open-source-link` (the click doesn't bubble to the
 * cover's inspect).
 *
 * Rich-hover-details wiring: a `mouseenter` on the card emits `hover` with this
 * candidate. The card does NOT fetch or debounce itself (still presentation
 * only) — the parent (Discover.vue → the discover page) debounces this event
 * and calls `useDiscover().loadDetails`, which merges author/artist/
 * description/genres back into the candidate once Suwayomi's forced details
 * fetch resolves; `<DiscoverHoverPreview>` below renders them as soon as they
 * land, with no extra wiring needed here.
 *
 * Presentation only: the candidate arrives via props; every action is emitted.
 */
const props = defineProps<{
  /** The candidate this card represents. */
  candidate: DiscoverCandidate
}>()

const emit = defineEmits<{
  /** Primary cover click — open the Adopt/Inspect flow for this candidate. */
  inspect: [candidate: DiscoverCandidate]
  /** "+ Adopt" click — open the Adopt flow with intent to adopt this candidate. */
  adopt: [candidate: DiscoverCandidate]
  /** "View on source ↗" clicked — the parent may react; the `<a>` still opens. */
  'open-source-link': [candidate: DiscoverCandidate]
  /** Cursor entered the card — the parent debounces this to trigger the
   *  on-demand rich-details fetch for the hover preview. */
  hover: [candidate: DiscoverCandidate]
}>()

// The big faint placeholder letter behind a cover (first char, uppercased).
const initial = (title: string): string => (title.trim()[0] ?? '?').toUpperCase()

// The "View on source" href — the browser-clickable realUrl, scheme-guarded via
// the shared safeHttpUrl since it comes from untrusted upstream source data.
// undefined (never rendered) for a missing or non-http(s) value — deliberately
// never falls back to the source-relative addressing `url`.
const safeSourceUrl = computed(() => safeHttpUrl(props.candidate.realUrl))

// "View on source" notifies the parent but does NOT preventDefault — the real
// `<a target="_blank">` still opens the source in a new tab (Bug-1 fix). Stop
// propagation so it doesn't also trigger the card's inspect.
const onSourceLink = (e: Event): void => {
  e.stopPropagation()
  emit('open-source-link', props.candidate)
}
</script>

<template>
  <div class="disc-card" @mouseenter="emit('hover', candidate)">
    <!-- Inner box is overflow-clipped; the popup is its SIBLING (never clipped) -->
    <div class="disc-card__box">
      <button
        type="button"
        class="disc-card__cover"
        :aria-label="`Inspect ${candidate.title}`"
        @click="emit('inspect', candidate)"
      >
        <div class="disc-card__placeholder">
          <span class="disc-card__initial">{{ initial(candidate.title) }}</span>
        </div>
        <img
          v-if="candidate.thumbnailUrl"
          class="disc-card__img"
          :src="candidate.thumbnailUrl"
          :alt="`${candidate.title} cover`"
          loading="lazy"
        >
        <div class="disc-card__scrim" />
        <Tag v-if="candidate.inLibrary" class="disc-card__inlib" tone="success">
          <template #icon>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M20 6L9 17l-5-5" />
            </svg>
          </template>
          IN LIBRARY
        </Tag>
        <div class="disc-card__title-wrap">
          <div class="disc-card__title">{{ candidate.title }}</div>
        </div>
      </button>

      <div class="disc-card__foot">
        <button type="button" class="adopt-btn" @click="emit('adopt', candidate)">+ Adopt</button>
        <a
          v-if="safeSourceUrl"
          class="source-link"
          :href="safeSourceUrl"
          target="_blank"
          rel="noopener noreferrer"
          @click="onSourceLink"
        >
          Source
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M7 17L17 7M17 7H8M17 7v9" />
          </svg>
        </a>
      </div>
    </div>

    <!-- Hover-preview popup (Bug-2 fix — sibling/absolute/pointer-events-none) -->
    <DiscoverHoverPreview :candidate="candidate" />
  </div>
</template>

<style scoped>
/* Discover-specific cover tokens (initial-letter glyph). The canonical global
 * home is index.css; imported here too so the card ships able to render on its
 * own. The :root defs are idempotent. */
@import '../../assets/css/tokens/discover.css';

/* 🔴 §3 CONTAINER QUERY: the card is a container (`inline-size`) so its OWN
 * WIDTH — not the viewport — drives the width-dependent sizing (title, foot
 * controls, the placeholder glyph). A card's width is
 * `tile = viewport × columns × ResponsiveGrid config`, which a media query
 * structurally cannot read (§3.2): the same card renders at a ~112-137px narrow
 * (tablet) tile, a ~141-196px 2-col phone tile, and a ≥172.6px desktop tile.
 * `container-type: inline-size` (NEVER `size`, §3.5 — that adds full size
 * containment and the card would collapse). Descendants query `@container
 * disc-card` below.
 *
 * GAP-090 SAFE: `.disc-card` is ALREADY the containing block for the
 * absolutely-positioned `.disc-pop` hover popup (via `position: relative`), so
 * `container-type`'s `contain: layout` does not move it — the popup geometry is
 * byte-identical and the hover preview is untouched. */
.disc-card {
  position: relative;
  /* QCAT-230: grid items default to a content-size minimum — without this a
   * narrow phone column (see Discover.vue's ResponsiveGrid) could refuse to
   * shrink below the card's intrinsic content width and overflow the grid. */
  min-width: 0;
  container-type: inline-size;
  container-name: disc-card;
}

/* Lift the whole card above its neighbours so the popup is never covered. */
.disc-card:hover {
  z-index: 40;
}

/* Reveal the owned hover-preview popup (the child's `.disc-pop` root carries this
 * parent scope, so the selector matches across the component boundary). */
.disc-card:hover .disc-pop {
  opacity: 1;
  visibility: visible;
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
  transform: translateY(-0.25rem); /* -4px @16 */
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

/* 🔴 §3 width-driven PLACEHOLDER GLYPH: the big faint initial letter scales with
 * the CARD's own width via `cqi`, capped at 3.625rem (58px @16) — so it no longer
 * stays a fixed 58px on a narrow tile. The `40cqi` term exceeds the cap for every
 * tile ≥145px, so EVERY desktop tile (min 172.6px) is capped at 3.625rem: at the
 * 16px anchor that is exactly 58px — byte-identical to the old fixed `58px`, and
 * (like every px→rem migration) it now also rides the fluid root off-anchor. On a
 * ~112px tablet tile it drops to ~45px so it fits. A11y ratio 3.625/1.5 = 2.42
 * ≤ 2.5 (§2.2/§3.7). */
.disc-card__initial {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: clamp(1.5rem, 40cqi, 3.625rem); /* 24px … 58px @16 */
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

/* Position the IN LIBRARY <Tag> over the cover; re-add the cover-chrome blur that
 * the success tone doesn't carry, so the marker frosts the cover behind it. */
.disc-card__inlib {
  position: absolute;
  top: var(--space-xs); /* 8px @16 */
  left: var(--space-xs);
  backdrop-filter: blur(4px); /* effect radius — stays raw px (§2.6 skill note) */
}

.disc-card__title-wrap {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: var(--space-sm); /* 10px @16 */
}

/* 🔴 §3 width-driven TITLE: `clamp(rem-floor, cqi, rem-cap)`. The `cqi` term
 * sizes it by the CARD's own width; the rem floor is the a11y anchor (§3.7 —
 * user font-size preference still flows through). The 0.8125rem (13px @16) CAP
 * is reached for every tile ≥155px, so EVERY desktop tile (min 172.6px) is
 * capped at 0.8125rem — identical to the old `var(--text-base)` (also
 * 0.8125rem) at EVERY desktop width, byte-identical at the 16px anchor. On a
 * narrow tablet/phone tile it steps down toward the 0.75rem (12px) floor. A11y
 * ratio 0.8125/0.75 = 1.083 ≤ 2.5 (§2.2). */
.disc-card__title {
  font-weight: var(--weight-bold);
  font-size: clamp(0.75rem, 4.5cqi + 0.375rem, 0.8125rem); /* 12px … 13px @16 */
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
  padding: var(--space-xs) 0.6875rem; /* 8px 11px @16 — 11px off-ladder literal */
  border-top: 1px solid var(--border);
}

.adopt-btn {
  padding: 0;
  border: none;
  background: none;
  color: var(--accentBright);
  font-family: var(--font-sans);
  font-size: 0.71875rem; /* 11.5px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.source-link {
  display: inline-flex;
  align-items: center;
  gap: 0.1875rem; /* 3px @16 */
  font-size: var(--text-xs);
  color: var(--faint);
  text-decoration: none;
  transition: color 0.15s;
}

.source-link:hover {
  color: var(--text);
}

/* 🔴 §3 NARROW-TILE step (discrete, §3.6 — the action foot has a fit FLOOR, not
 * a curve). Fires by the CARD's OWN width: a tile ≤150px is only ever a narrow
 * tablet column (~112-137px) or a small-phone 2-col tile (~141px at 320px).
 * Desktop tiles are min 172.6px (min-tile 184px at the 901px root) → this NEVER
 * fires on desktop (22.6px margin, byte-identical). It tightens the action
 * foot's two controls (+Adopt / Source) to the badge floor and pulls the
 * paddings in so both fit a ~112px tile without overflow. Magnitudes still ride
 * the fluid root. */
@container disc-card (max-width: 150px) {
  .disc-card__title-wrap {
    padding: var(--space-xs); /* 8px @16 */
  }

  .disc-card__foot {
    padding: var(--space-xs) var(--space-sm); /* 8px 10px @16 */
  }

  .adopt-btn,
  .source-link {
    font-size: var(--text-2xs); /* 9.5px @16 — the badge floor */
  }
}
</style>
