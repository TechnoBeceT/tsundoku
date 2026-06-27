<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import type { Extension } from '../screens/settings.types'

/**
 * ExtensionRow — one Suwayomi source-extension card. A deterministic id-tinted
 * avatar, the name + language + version, an optional UPDATE badge, and the
 * action button(s): an installed row shows Update (when an update exists) +
 * Uninstall; an available row shows Install. The acting button spins + disables
 * while this row's mutation is in flight (§16).
 *
 *   - `extension`: the extension to render.
 *   - `installed`: installed variant (Update/Uninstall) vs available (Install).
 *   - `busy`: this row's mutation is in flight.
 *
 * Emits `install` / `update` / `uninstall` (the parent dispatches by pkgName).
 */
const props = withDefaults(defineProps<{
  /** The extension to render. */
  extension: Extension
  /** Installed variant (Update/Uninstall) when true, else Available (Install). */
  installed?: boolean
  /** This row's mutation is in flight. */
  busy?: boolean
}>(), {
  installed: false,
  busy: false,
})

const emit = defineEmits<{
  /** Install this available extension. */
  'install': []
  /** Update this installed extension. */
  'update': []
  /** Uninstall this installed extension (routed through a confirm by the parent). */
  'uninstall': []
}>()

// A deterministic accent hue for the square avatar (pkgName-derived).
const hue = computed(() => {
  let h = 0
  for (let i = 0; i < props.extension.id.length; i++) h = (h * 31 + props.extension.id.charCodeAt(i)) % 360
  return h
})
const language = computed(() => props.extension.lang.toUpperCase())
</script>

<template>
  <div class="ext-card" :class="{ 'ext-card--busy': busy }">
    <span class="ext-card__avatar" :style="{ background: `hsl(${hue} 55% 30%)` }" />
    <div class="ext-card__body">
      <div class="ext-card__titleline">
        <span class="ext-card__name">{{ extension.name }}</span>
        <span class="ext-card__lang">{{ language }}</span>
        <span v-if="installed && extension.hasUpdate" class="update-badge">UPDATE</span>
      </div>
      <div class="ext-card__version">v{{ extension.version }}</div>
    </div>

    <template v-if="installed">
      <AppButton v-if="extension.hasUpdate" variant="solid" size="sm" :loading="busy" @click="emit('update')">Update</AppButton>
      <AppButton variant="danger-ghost" size="sm" :loading="busy" @click="emit('uninstall')">Uninstall</AppButton>
    </template>
    <AppButton v-else variant="mini" size="sm" :loading="busy" @click="emit('install')">
      <template #icon>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
      </template>
      Install
    </AppButton>
  </div>
</template>

<style scoped>
.ext-card {
  display: flex;
  align-items: center;
  gap: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 12px 14px;
}

/* In-flight row dims + blocks pointer input while its mutation runs (§16). */
.ext-card--busy {
  opacity: 0.6;
  pointer-events: none;
}

.ext-card__avatar {
  width: 34px;
  height: 34px;
  border-radius: var(--radius-md);
  flex: none;
}

.ext-card__body {
  flex: 1;
  min-width: 0;
}

.ext-card__titleline {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ext-card__name {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
}

.ext-card__lang {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  padding: 1px 6px;
  border-radius: var(--radius-xs);
  background: var(--surface3);
  color: var(--muted);
}

.update-badge {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  background: var(--set-update-bg);
  color: var(--set-update-text);
}

.ext-card__version {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--faint);
  margin-top: 2px;
}
</style>
