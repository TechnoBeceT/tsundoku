<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { countLabel, resolveOpen } from './DisclosurePanel.logic'

/**
 * DisclosurePanel — the app's ONE collapse/expand panel shell (QCAT-265,
 * treatment #2 of the per-place scroll model): a `--surface` panel with a
 * divided header (display-font title, optional count badge + summary, optional
 * `lead`/`actions` groups) over a FULL-BLEED body. When `collapsible`, the title
 * becomes a real `<button>` that shows/hides the body — "click and the whole list
 * shows up; close it and you get the space back" (the owner's words).
 *
 * SCROLL MODEL (QCAT-265 §2.6). This is treatment #2: the DISCLOSURE, for a long
 * list that is IN THE WAY (the owner's two places: exclude-sources in
 * match-series, and add-source). It GROWS WITH ITS CONTENT and the DOCUMENT
 * scrolls; a long list is tamed by COLLAPSING it, never by letterboxing it into a
 * nested `overflow-y:auto` band. So this shell sets NO height and NO overflow of
 * its own — that is the point of the treatment; do not add one. A viewport-keyed
 * bound (`calc(100dvh − …)`) is BANNED here as everywhere (§2.6.3).
 *
 * 🔴 NOT the bounded inner-scroller. Treatment #1 (a real `max-height` inner-
 * scroll for the SIDE-BY-SIDE + ASYMMETRIC Series-Detail Chapters/Sources panels,
 * per the asymmetry-AND-empty-space diagnostic) is a SEPARATE capability and
 * lives on `seriesDetail/PanelCard.vue` (its `max-height` prop). DisclosurePanel
 * and PanelCard are sibling panel shells sharing the divided-header look but
 * answering different scroll questions — do not fold one into the other.
 *
 * Per QCAT-259 (fix once, parameterise the differences) this is the single
 * disclosure in the app — never hand-roll a per-screen open/close.
 *
 *   - `title`: the panel heading (omit for a header-less panel).
 *   - `count`: a count badge shown beside the title (0 shows; null/undefined hides).
 *   - `summary`: quiet secondary text in the header (e.g. "3 of 40 selected"),
 *     useful when collapsed since the body is hidden.
 *   - `collapsible` (default false): render the trigger + allow hiding the body.
 *   - `defaultOpen` (default true): the UNCONTROLLED starting state.
 *   - `open`: drive the state from the host (`v-model:open`); omit for uncontrolled.
 *   - `flat` (default false): drop the surface/border/radius chrome and the header
 *     rule — for a disclosure nested INSIDE an existing card (the source-filter
 *     chip cloud), where a second card outline would read as a box-in-a-box.
 *
 * Slots:
 *   - default: the full-bleed body (the shell adds no body padding).
 *   - `lead`: header-left content grouped with the title. NOTE: with
 *     `collapsible` the title lives inside the trigger button — put interactive
 *     content in `actions`, never `lead` (a button cannot nest a button).
 *   - `actions`: header-right content, OUTSIDE the trigger, so its buttons stay
 *     independently clickable.
 *
 * Accessibility: the trigger is a real `<button type="button">` (keyboard- and
 * screen-reader-native) carrying `aria-expanded` + `aria-controls` pointing at
 * the body region's id.
 */
const props = withDefaults(defineProps<{
  /** Panel heading shown in the divided header (omit for a header-less panel). */
  title?: string
  /** Count badge beside the title; `0` renders, null/undefined hides the badge. */
  count?: number | string | null
  /** Quiet secondary header text (stays visible while collapsed). */
  summary?: string
  /** Render the header title as an open/close trigger. */
  collapsible?: boolean
  /** Starting open state when the host does not control `open`. */
  defaultOpen?: boolean
  /** Controlled open state (`v-model:open`); omit for uncontrolled. */
  open?: boolean | null
  /** Drop the card chrome — for a disclosure nested inside another card. */
  flat?: boolean
}>(), {
  title: '',
  count: null,
  summary: '',
  collapsible: false,
  defaultOpen: true,
  open: null,
  flat: false,
})

const emit = defineEmits<{
  /** The open state changed (`v-model:open`); also fires in uncontrolled mode. */
  'update:open': [open: boolean]
}>()

// Uncontrolled state. `resolveOpen` decides whether this or the `open` prop wins.
const localOpen = ref(props.defaultOpen)

const isOpen = computed(() => resolveOpen(props.collapsible, props.open, localOpen.value))
const badge = computed(() => countLabel(props.count))

// A stable, collision-free id so `aria-controls` can address this panel's body
// even when several disclosures share a screen.
const regionId = `disclosure-${useId()}`

const toggle = (): void => {
  const next = !isOpen.value
  localOpen.value = next
  emit('update:open', next)
}
</script>

<template>
  <section class="dp" :class="{ 'dp--flat': flat }">
    <div v-if="title || $slots.lead || $slots.actions" class="dp__head">
      <div class="dp__headleft">
        <!-- Collapsible: the title IS the trigger, so the whole heading is a
             generous tap target (QCAT-230's 44px bar) rather than a lone chevron. -->
        <button
          v-if="collapsible"
          type="button"
          class="dp__trigger"
          :aria-expanded="isOpen"
          :aria-controls="regionId"
          @click="toggle"
        >
          <svg
            class="dp__chevron"
            :class="{ 'dp__chevron--open': isOpen }"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2.6"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M9 18l6-6-6-6" />
          </svg>
          <span v-if="title" class="dp__title">{{ title }}</span>
          <span v-if="badge" class="dp__count">{{ badge }}</span>
          <span v-if="summary" class="dp__summary">{{ summary }}</span>
        </button>

        <template v-else>
          <span v-if="title" class="dp__title">{{ title }}</span>
          <span v-if="badge" class="dp__count">{{ badge }}</span>
          <span v-if="summary" class="dp__summary">{{ summary }}</span>
        </template>

        <slot name="lead" />
      </div>
      <div v-if="$slots.actions" class="dp__headright">
        <slot name="actions" />
      </div>
    </div>

    <!-- v-show, not v-if: collapsing must not tear down the body's state (a
         half-typed filter, a row's expanded inspect list) — reopening restores
         exactly what was there. -->
    <div v-show="isOpen" :id="regionId" class="dp__content">
      <slot />
    </div>
  </section>
</template>

<style scoped>
/* 🔴 NO height / max-height / overflow-scroll here, deliberately (QCAT-265
 * treatment #2): the panel grows with its content and the DOCUMENT scrolls. A
 * nested scroll band is what this component exists to replace. (The `overflow:
 * hidden` below is corner-CLIPPING, not scrolling.) */
.dp {
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  /* Clip the body's first/last rows to the rounded corner. */
  overflow: hidden;
  /* The flex/grid overflow trap: lets this panel shrink below its content width
   * inside a grid/flex parent instead of forcing horizontal overflow (QCAT-230). */
  min-width: 0;
}

/* Nested inside an existing card — no second outline, no surface fill. */
.dp--flat {
  border: none;
  border-radius: 0;
  background: none;
  overflow: visible;
}

.dp__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5625rem; /* 9px @16 — byte-identical at the anchor, fluid below/above */
  padding: 0.9375rem var(--space-xl); /* 15px 18px @16 (15px has no ladder step) */
  border-bottom: 1px solid var(--border);
}

.dp--flat .dp__head {
  padding: 0 0 var(--space-sm);
  border-bottom: none;
}

.dp__headleft {
  display: flex;
  align-items: center;
  gap: 0.5625rem; /* 9px @16 */
  min-width: 0;
}

/* The trigger is a button but wears the header's own look — the chevron is the
 * affordance. Negative margins keep the enlarged 44px tap target from shifting
 * the title off the header's padding grid; they are 44px-touch geometry, so they
 * stay raw px (like the target itself), not the fluid scale. */
.dp__trigger {
  display: flex;
  align-items: center;
  gap: 0.5625rem; /* 9px @16 */
  min-width: 0;
  min-height: 44px;
  margin: -11px 0 -11px -6px;
  padding: 0 6px;
  border: none;
  border-radius: var(--radius-md);
  background: none;
  color: inherit;
  font-family: inherit;
  cursor: pointer;
  text-align: left;
}

.dp__trigger:hover .dp__title {
  color: var(--accentBright);
}

.dp__trigger:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.dp__chevron {
  flex: none;
  color: var(--faint);
  transition: transform 0.15s ease;
}

.dp__chevron--open {
  transform: rotate(90deg);
}

.dp__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 0.9375rem; /* 15px @16 — the panel title (no ladder step at 15px) */
  color: var(--text);
  transition: color 0.15s;
  /* A long title wraps rather than pushing the header past the viewport (QCAT-230). */
  min-width: 0;
  overflow-wrap: anywhere;
}

.dp__count {
  flex: none;
  padding: 1px var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.dp__summary {
  min-width: 0;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--faint);
  overflow-wrap: anywhere;
}

.dp__headright {
  display: flex;
  align-items: center;
  gap: 0.5625rem; /* 9px @16 */
  flex: none;
}

@media (max-width: 900px) {
  /* `.dp__headright`'s `flex: none` action buttons used to squeeze
   * `.dp__headleft` to nothing on a phone, truncating even a short title. Wrap
   * the header and give the actions their own full-width row: the title keeps its
   * natural width on line 1 and the buttons stay tappable on line 2 (QCAT-261's
   * "stack the heavy rows").
   *
   * `row-gap: var(--touch-pitch)` is a TOUCH-TARGET rule, not styling: once these
   * wrap, the action buttons' invisible 44px hit overlays face each other across
   * the gap and overlap it, making the space between two buttons ambiguous to tap
   * (the add-source place stacks several actions here). The pitch is the gap that
   * keeps each target its own. */
  .dp__head {
    flex-wrap: wrap;
    row-gap: var(--touch-pitch);
  }

  .dp__headright {
    flex: 1 0 100%;
    flex-wrap: wrap;
    row-gap: var(--touch-pitch);
    justify-content: flex-end;
  }
}
</style>
