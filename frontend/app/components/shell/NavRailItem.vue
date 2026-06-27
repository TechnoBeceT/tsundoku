<script setup lang="ts">
import type { NavBadge } from './types'

/**
 * NavRailItem — one icon button in the AppShell nav rail: a bare-lucide icon, an
 * optional count badge, and an active (selected) treatment. Used for BOTH the
 * primary nav items and the bottom-pinned ones (e.g. Settings) — the rail owns
 * placement + spacing; this atom owns the button look, badge, and a11y.
 *
 * The button is icon-only, so `label` doubles as its `title` + `aria-label`;
 * `active` adds the highlight and `aria-current="page"`. Emits `select` on click.
 */
withDefaults(defineProps<{
  /** BARE lucide icon name (e.g. "book") — the "lucide:" prefix is added here. */
  icon: string
  /** Visible label — used as the button's `title` + `aria-label`. */
  label: string
  /** Whether this item is the active route (highlight + `aria-current`). */
  active?: boolean
  /** Optional count pill (hidden when its count is 0); see `NavBadge`. */
  badge?: NavBadge
}>(), {
  active: false,
  badge: undefined,
})

const emit = defineEmits<{
  /** The item was activated (clicked). */
  select: []
}>()
</script>

<template>
  <button
    type="button"
    class="item"
    :class="{ 'item--active': active }"
    :title="label"
    :aria-label="label"
    :aria-current="active ? 'page' : undefined"
    @click="emit('select')"
  >
    <Icon :name="`lucide:${icon}`" class="item__icon" />
    <span
      v-if="badge && badge.count > 0"
      class="item__badge"
      :class="`item__badge--${badge.tone ?? 'danger'}`"
    >{{ badge.count }}</span>
  </button>
</template>

<style scoped>
.item {
  position: relative;
  width: 46px;
  height: 46px;
  border-radius: var(--radius-xl);
  border: 1px solid transparent;
  background: transparent;
  color: var(--muted);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: all 0.15s;
}

.item:hover {
  color: var(--text);
  background: var(--surface2);
}

.item--active {
  border-color: var(--border2);
  background: var(--accentSoft);
  color: var(--accentBright);
}

.item:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.item__icon {
  width: 22px;
  height: 22px;
}

.item__badge {
  position: absolute;
  top: -4px;
  right: -4px;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: var(--radius-pill);
  font-size: 10.5px;
  font-weight: var(--weight-extrabold);
  display: flex;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--rail);
}

.item__badge--danger {
  background: var(--danger);
  color: var(--on-danger);
}

.item__badge--warn {
  background: var(--warn);
  color: var(--on-warn);
}
</style>
