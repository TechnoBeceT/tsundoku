<script setup lang="ts">
/**
 * SurfaceCard — the shared titled surface-card shell. A `--surface` panel with a
 * `--border` outline and the `--radius-2xl` corner, an optional padded header
 * (display-font title + faint sub), and the body in the default slot. The eight
 * settings/library/health cards that each hand-rolled `.card`/`.card__title`/
 * `.card__sub` collapse onto this one shell so the panel look lives in one place.
 *
 *   - `title`: the card heading (omit for a header-less / custom-header card).
 *   - `sub`: the faint one-line description under the title.
 *
 * Slots:
 *   - default: the card body (each consumer keeps its own body markup).
 *   - `header`: replace the whole built-in header (title/sub/actions row).
 *   - `actions`: header-right content (a toggle, a badge, a button) laid out
 *     across from the title.
 */
defineProps<{
  /** Card heading shown in the header (omit for a header-less card). */
  title?: string
  /** Faint one-line description under the title. */
  sub?: string
}>()
</script>

<template>
  <section class="surface-card">
    <!-- The whole header is overridable; otherwise it's title + sub on the left
         and the optional `actions` slot across from them. -->
    <slot name="header">
      <div v-if="title || sub || $slots.actions" class="surface-card__head">
        <div class="surface-card__heading">
          <h2 v-if="title" class="surface-card__title">{{ title }}</h2>
          <p v-if="sub" class="surface-card__sub">{{ sub }}</p>
        </div>
        <div v-if="$slots.actions" class="surface-card__actions">
          <slot name="actions" />
        </div>
      </div>
    </slot>
    <slot />
  </section>
</template>

<style scoped>
.surface-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 20px;
}

.surface-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  /* The head owns the rhythm before the body — moved here off `.surface-card__sub`
     so a TITLE-ONLY card (e.g. the Series-Detail Trackers section, whose header
     carries the "Reset progress / Sync now" actions) is never flush against its
     content. When a sub IS present its own bottom margin is dropped, so this 8px
     stays the single, identical head→body gap in both cases. */
  margin-bottom: 8px;
}

.surface-card__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  margin: 0;
}

.surface-card__sub {
  font-size: 12.5px;
  color: var(--faint);
  /* No bottom margin — the head now owns the head→body rhythm (see above), so
     the gap is identical whether or not a sub is present. */
  margin: 2px 0 0;
}

.surface-card__heading {
  min-width: 0;
}

.surface-card__actions {
  flex: none;
}
</style>
