<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import type { Extension } from '../screens/settings.types'

/**
 * ExtensionRow — one Suwayomi source-extension card. The extension's own icon
 * (proxied same-origin, see useExtensions' iconUrl) when available, else a
 * deterministic id-tinted placeholder square; the name + language + version,
 * an optional UPDATE badge, and the action button(s): an installed row shows
 * Update (when an update exists) + Uninstall; an available row shows Install.
 * The acting button spins + disables while this row's mutation is in flight
 * (§16).
 *
 *   - `extension`: the extension to render.
 *   - `installed`: installed variant (Update/Uninstall) vs available (Install).
 *   - `busy`: this row's mutation is in flight.
 *
 * Installed rows also show a Configure (gear) button that opens the per-source
 * preferences dialog.
 *
 * Emits `install` / `update` / `uninstall` / `configure` (the parent dispatches
 * by pkgName).
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
  /** Open this installed extension's per-source preferences dialog. */
  'configure': []
  /** Reinstall (roll back to) a HELD version of this installed extension. */
  'reinstall': [versionCode: number]
}>()

// A deterministic accent hue for the placeholder square (pkgName-derived).
const hue = computed(() => {
  let h = 0
  for (let i = 0; i < props.extension.id.length; i++) h = (h * 31 + props.extension.id.charCodeAt(i)) % 360
  return h
})
const language = computed(() => props.extension.lang.toUpperCase())

// ---- Version history (reversible updates) ---------------------------------
// The held (cached) versions the owner can reinstall (roll back to). Shown only
// on installed rows behind a toggle, so the card stays compact by default. A
// history is worth showing only when there is MORE than the current version.
const heldVersions = computed(() => props.extension.cachedVersions ?? [])
const hasHistory = computed(() => props.installed && heldVersions.value.length > 1)
const showHistory = ref(false)

// Formats a cachedAt ISO timestamp as a short local date; a bad value renders
// nothing rather than "Invalid Date".
function formatCachedAt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
}

const isCurrentVersion = (versionCode: number): boolean => versionCode === props.extension.versionCode

// The tinted square is the fallback: an empty iconUrl (e.g. a backend-less
// Storybook fixture) or a load failure (@error, e.g. a 404/502 from the proxy)
// both fall back to it, so the row never shows a broken-image icon.
const iconFailed = ref(false)
watch(() => props.extension.iconUrl, () => { iconFailed.value = false })
const showIcon = computed(() => !!props.extension.iconUrl && !iconFailed.value)
</script>

<template>
  <div class="ext-item">
    <div class="ext-card" :class="{ 'ext-card--busy': busy }">
      <img
        v-if="showIcon"
        :src="extension.iconUrl"
        alt=""
        aria-hidden="true"
        loading="lazy"
        class="ext-card__avatar ext-card__icon"
        @error="iconFailed = true"
      >
      <span v-else class="ext-card__avatar" :style="{ background: `hsl(${hue} 55% 30%)` }" />
      <div class="ext-card__body">
        <div class="ext-card__titleline">
          <span class="ext-card__name">{{ extension.name }}</span>
          <span class="ext-card__lang">{{ language }}</span>
          <span v-if="installed && extension.hasUpdate" class="update-badge">UPDATE</span>
        </div>
        <div class="ext-card__version">v{{ extension.version }}</div>
      </div>

      <template v-if="installed">
        <AppButton
          v-if="hasHistory"
          variant="mini"
          size="sm"
          :disabled="busy"
          :aria-expanded="showHistory"
          @click="showHistory = !showHistory"
        >
          {{ showHistory ? 'Hide history' : `History (${heldVersions.length})` }}
        </AppButton>
        <AppButton variant="mini" size="sm" :disabled="busy" @click="emit('configure')">
          <template #icon>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>
          </template>
          Configure
        </AppButton>
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

    <!-- Version history: HELD .apk versions the owner can reinstall (roll back
         to). Installed rows only; collapsed by default (§ reversible updates). -->
    <div v-if="hasHistory && showHistory" class="ext-history">
      <div v-for="cv in heldVersions" :key="cv.versionCode" class="ext-history__row">
        <span class="ext-history__ver">v{{ cv.versionName || cv.versionCode }}</span>
        <span v-if="formatCachedAt(cv.cachedAt)" class="ext-history__date">cached {{ formatCachedAt(cv.cachedAt) }}</span>
        <span v-if="isCurrentVersion(cv.versionCode)" class="ext-history__current">Current</span>
        <AppButton
          v-else
          variant="mini"
          size="sm"
          :loading="busy"
          @click="emit('reinstall', cv.versionCode)"
        >
          Reinstall this version
        </AppButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* The grid item: the card plus its (optional) expanded version history. */
.ext-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

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

/* The real icon (an <img>, not the tinted placeholder <span>): contain so a
   non-square source icon doesn't stretch, and a neutral surface backdrop so a
   transparent-background icon reads cleanly. */
.ext-card__icon {
  object-fit: contain;
  background: var(--surface3);
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

/* ---- Version history (reversible updates) --------------------------------- */
.ext-history {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 12px;
  background: var(--surface2, var(--surface));
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.ext-history__row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.ext-history__ver {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.ext-history__date {
  flex: 1;
  font-size: var(--text-xs);
  color: var(--faint);
}

.ext-history__current {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
}

@media (max-width: 900px) {
  /* An installed row can carry Configure + Update + Uninstall — three buttons
   * beside the icon + name/lang/version body has no room on a phone. Wrap the
   * buttons onto their own line under the body rather than crushing it
   * (QCAT-230). */
  .ext-card {
    flex-wrap: wrap;
  }

  .ext-card__body {
    flex-basis: 160px;
  }
}
</style>
