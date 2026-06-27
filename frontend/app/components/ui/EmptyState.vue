<script setup lang="ts">
/**
 * EmptyState — a centered empty/placeholder block for a screen or panel that has
 * nothing to show yet (no series, no downloads, all-clear health, no categories,
 * an empty source listing). Renders an optional icon in a round tinted disc, a
 * `title`, an optional `sub` line, and an optional action below.
 *
 * Props:
 *   - `title`    — the headline (required).
 *   - `sub`      — optional secondary line under the title.
 *   - `iconTone` — token NAME (without the `--`) used to tint the icon, e.g.
 *                  "accentBright" or "set-ok-dot"; defaults to "muted".
 * Slots:
 *   - `icon`     — the glyph to show in the disc (a lucide `<Icon>` or a
 *                  `<BrandMark>`); the disc is hidden when this slot is empty.
 *   - default    — optional action (e.g. an `<AppButton>`), shown under the text.
 */
withDefaults(defineProps<{
  /** The headline text. */
  title: string
  /** Optional secondary line under the title. */
  sub?: string
  /** Token name (no `--`) used to tint the icon. */
  iconTone?: string
}>(), {
  sub: undefined,
  iconTone: 'muted',
})
</script>

<template>
  <div class="empty">
    <div
      v-if="$slots.icon"
      class="empty__icon"
      :style="{ color: `var(--${iconTone})` }"
    >
      <slot name="icon" />
    </div>
    <div class="empty__title">{{ title }}</div>
    <p v-if="sub" class="empty__sub">{{ sub }}</p>
    <div v-if="$slots.default" class="empty__action">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 52px 24px;
}

.empty__icon {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 16px;
  background: var(--surface3);
}

.empty__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
}

.empty__sub {
  margin: 6px 0 0;
  font-size: var(--text-base);
  color: var(--muted);
}

.empty__action {
  margin-top: 18px;
}
</style>
