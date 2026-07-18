<script setup lang="ts">
import type { SettingsPane } from '../screens/settings.types'

/**
 * SettingsNav — the sticky sidebar that switches the Settings screen between its
 * panes. Presentation-only: the active pane arrives via `active` and every
 * click is emitted as `select` (the screen owns the controlled pane state).
 *
 *   - `active`: the currently-showing pane (drives the highlighted item).
 *
 * Emits `select` with the picked pane key.
 */
defineProps<{
  /** The pane currently showing (highlighted in the list). */
  active: SettingsPane
}>()

const emit = defineEmits<{
  /** A pane was picked from the sidebar — carries its key. */
  select: [pane: SettingsPane]
}>()

// The panes in display order, with their sidebar labels.
const panes: { key: SettingsPane, label: string }[] = [
  { key: 'library', label: 'Schedules & Behavior' },
  { key: 'categories', label: 'Categories' },
  { key: 'engine', label: 'Engine' },
  { key: 'serverConfig', label: 'Server config' },
  { key: 'extensions', label: 'Sources & Extensions' },
  { key: 'sources', label: 'Sources' },
  { key: 'network', label: 'Network' },
  { key: 'trackers', label: 'Trackers' },
  { key: 'notifications', label: 'Notifications' },
]
</script>

<template>
  <nav class="nav">
    <button
      v-for="p in panes"
      :key="p.key"
      type="button"
      class="nav__item"
      :class="{ 'nav__item--active': active === p.key }"
      @click="emit('select', p.key)"
    >
      {{ p.label }}
    </button>
  </nav>
</template>

<style scoped>
.nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
  position: sticky;
  top: 24px;
}

.nav__item {
  display: flex;
  align-items: center;
  padding: 10px 13px;
  border-radius: var(--radius-lg);
  border: none;
  background: transparent;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  text-align: left;
  transition: all 0.15s;
}

.nav__item:hover {
  color: var(--text);
}

.nav__item--active {
  background: var(--accentSoft);
  color: var(--accentBright);
}

@media (max-width: 900px) {
  /* A tall sticky vertical list has nowhere to stick usefully once Settings.vue
   * stacks the sidebar above the pane (it would pin mid-scroll and overlap the
   * pane content below it). Becomes a wrapping row of pills instead — the
   * "top row of tabs" shape QCAT-230/231 call for on narrow layouts. */
  .nav {
    flex-direction: row;
    flex-wrap: wrap;
    position: static;
    top: auto;
    gap: 6px;
  }

  .nav__item {
    padding: 8px 12px;
    font-size: 13px;
  }
}
</style>
