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
    <slot />
  </section>
</template>

<style scoped>
.panel {
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
  min-width: 0;
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
</style>
