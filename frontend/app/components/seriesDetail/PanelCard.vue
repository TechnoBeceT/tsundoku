<script setup lang="ts">
/**
 * PanelCard — the shared Series-Detail panel shell: a `--surface` panel with a
 * `--border` outline and the `--radius-2xl` corner, a divided header (its own
 * padding + a `--border` bottom rule) carrying the display-font title, and a
 * FULL-BLEED body in the default slot (the shell adds no body padding, so each
 * consumer keeps its own scroll/padding). The Chapters + Sources cards that each
 * hand-rolled `.panel`/`.panel__head`/`.panel__title` collapse onto this one
 * shell so the divided-panel look lives in one place. (This is the header-divided,
 * full-bleed sibling of the ui/SurfaceCard shell — that one is padded + rule-less.)
 *
 *   - `title`: the panel heading (omit for a header-less panel).
 *
 * Slots:
 *   - default: the full-bleed body (each consumer keeps its own body markup).
 *   - `lead`: header-left content shown immediately after the title, grouped with
 *     it (e.g. a count pill that belongs beside the heading — the Sources card).
 *   - `actions`: header-right content laid out across from the title (a count
 *     pill on its own — the Chapters card — or an add button — the Sources card).
 *
 * SCROLL SHAPE (Series-Detail viewport-bounded panels): this shell is a fixed
 * header over an internally-scrolling body. `.panel` fills whatever height its
 * parent grid cell allots it and is itself a flex column; `.panel__head` stays
 * fixed size, and `.panel__content` (wrapping the default slot) takes the rest
 * of the height and scrolls on its own — the ONE scroll container both
 * ChaptersPanel and SourcesPanel get "for free" (neither sets its own
 * overflow/max-height any more). See the min-height:0 comments below — this
 * is the same flex/grid overflow trap as the Series-Detail `.columns` grid,
 * one level deeper.
 */
defineProps<{
  /** Panel heading shown in the divided header (omit for a header-less panel). */
  title?: string
}>()
</script>

<template>
  <section class="panel">
    <!-- The divided header: title + its `lead` group on the left, `actions` across
         from them on the right. Rendered only when there's something to show. -->
    <div v-if="title || $slots.lead || $slots.actions" class="panel__head">
      <div class="panel__headleft">
        <span v-if="title" class="panel__title">{{ title }}</span>
        <slot name="lead" />
      </div>
      <div v-if="$slots.actions" class="panel__headright">
        <slot name="actions" />
      </div>
    </div>
    <div class="panel__content">
      <slot />
    </div>
  </section>
</template>

<style scoped>
.panel {
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
  min-width: 0;
  display: flex;
  flex-direction: column;
  /* Fills the height the parent grid cell stretches this item to (see
   * SeriesDetail's `.columns`). 🔴 min-height: 0 is the SAME overflow trap
   * as `.columns` itself, one level down: a grid ITEM's automatic minimum
   * height is its content size, so without this override `.panel` (and the
   * grid row with it) would refuse to shrink below its content — the same
   * unbounded-scrollbar failure, just at the panel level instead of the page
   * level. Outside the bounded Series-Detail grid (e.g. a Storybook frame
   * with no fixed-height ancestor) height:100% simply resolves to auto, so
   * this never breaks an unbounded story. */
  height: 100%;
  min-height: 0;
}

.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 9px;
  padding: 15px 18px;
  border-bottom: 1px solid var(--border);
}

.panel__headleft {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.panel__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 15px;
  color: var(--text);
}

.panel__headright {
  display: flex;
  align-items: center;
  gap: 9px;
  flex: none;
}

/* The scrolling body — the same shape the Chapters card already had, now
 * shared by both panels. flex: 1 takes whatever height `.panel` has left
 * after the fixed-size header above. 🔴 min-height: 0 here is the SAME
 * overflow trap yet another level down (a flex ITEM's automatic minimum
 * height is its content size) — without it this body would grow to fit
 * every row instead of scrolling, and the page-level scrollbar comes back.
 * Three nested applications of the identical rule (.columns → .panel →
 * .panel__content) is not redundancy; each is a distinct flex/grid
 * container/item pair and each one independently re-triggers the trap. */
.panel__content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

@media (max-width: 900px) {
  /* `.panel__headright`'s `flex: none` action buttons (e.g. Sources' "Remove
   * duplicate files" + "Add") used to squeeze `.panel__headleft` down to
   * nothing on a phone, truncating even a short title like "Sources" to
   * "So…". Wrapping the header and forcing the actions group onto its own
   * full-width row gives the title its natural width on line 1 (never
   * truncated) and keeps the buttons tappable on line 2 instead of
   * overflowing/crushing line 1. */
  .panel__head {
    flex-wrap: wrap;
  }

  .panel__headright {
    flex: 1 0 100%;
    /* Sources' header-right can carry up to 3 buttons ("Remove duplicate
     * files" / "Remove fractional files" / "Add") — too wide for one line on
     * a phone even alone. Let THEM wrap too rather than overflow. */
    flex-wrap: wrap;
    justify-content: flex-end;
  }
}
</style>
