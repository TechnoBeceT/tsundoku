<script setup lang="ts">
import SettingsNav from '../settings/SettingsNav.vue'
import LibraryPane from '../settings/LibraryPane.vue'
import CategoriesPane from '../settings/CategoriesPane.vue'
import EnginePane from '../settings/EnginePane.vue'
import SuwayomiPane from '../settings/SuwayomiPane.vue'
import ExtensionsPane from '../settings/ExtensionsPane.vue'
import type {
  DurationValue,
  EngineInfo,
  Extension,
  LibrarySettings,
  Repo,
  ReorderDirection,
  RowActionState,
  SaveState,
  SettingsCategory,
  SettingsPane,
  SuwayomiConfig,
  SystemInfo,
  UpgradeStep,
} from './settings.types'

/**
 * Settings — the single-owner control panel. A thin container: a sticky sidebar
 * nav (SettingsNav) plus the one active pane, each pane extracted into its own
 * organism under `components/settings/`:
 *   - library     → LibraryPane     (schedules + read-only System)
 *   - categories  → CategoriesPane   (user-definable category CRUD)
 *   - engine      → EnginePane       (read-only status + upgrade stepper)
 *   - suwayomi    → SuwayomiPane      (proxied SOCKS + FlareSolverr config)
 *   - extensions  → ExtensionsPane    (installed / available / repositories)
 *
 * Presentation only: ALL state arrives via props and every mutation is emitted —
 * the panes own their local editable copies (§16 round-trip) and re-emit each
 * action, which this container forwards up unchanged. This screen owns only the
 * grid layout, the controlled `activePane`, and the loading skeletons; the pane
 * content + CSS live in the organisms. Token-only colours → both themes.
 */
withDefaults(defineProps<{
  /** Which pane is showing (controlled — the sidebar emits `set-pane`). */
  activePane?: SettingsPane
  /** The runtime-editable library knobs (2a). */
  library: LibrarySettings
  /** Read-only deploy-time facts for the System card (2a). */
  system: SystemInfo
  /** §16 state of the library Save button. */
  librarySave?: SaveState
  /** The user-defined category list (2b). */
  categories: SettingsCategory[]
  /** §16 state of category mutations (add/rename/reorder/delete): busy row + error. */
  categoryAction?: RowActionState
  /** Read-only engine status (2c). */
  engine: EngineInfo
  /** The upgrade stepper's steps (SSE-driven); empty = no upgrade started. */
  upgradeSteps?: UpgradeStep[]
  /** Whether an engine upgrade is currently running. */
  upgrading?: boolean
  /** The proxied Suwayomi server config (2d). */
  suwayomi: SuwayomiConfig
  /** §16 state of the Suwayomi Save button. */
  suwayomiSave?: SaveState
  /** Installed extensions (2e). */
  extensions: Extension[]
  /** Available (installable) extensions (2e). */
  availableExtensions: Extension[]
  /** Extension repository URLs (2e). */
  repos: Repo[]
  /** §16 state of extension mutations (install/update/uninstall): busy pkgName + error. */
  extensionAction?: RowActionState
  /** §16 state of repo mutations (add/remove/reorder): busy id + error. */
  repoAction?: RowActionState
  /** Background extension update-check cadence (2e). */
  extCheckInterval: DurationValue
  /** Whether a "check for updates" call is in flight. */
  checkingUpdates?: boolean
  /** When true, the whole screen renders as skeletons. */
  loading?: boolean
}>(), {
  activePane: 'library',
  librarySave: () => ({ status: 'idle' }),
  categoryAction: () => ({ busyId: null }),
  upgradeSteps: () => [],
  upgrading: false,
  suwayomiSave: () => ({ status: 'idle' }),
  extensionAction: () => ({ busyId: null }),
  repoAction: () => ({ busyId: null }),
  checkingUpdates: false,
  loading: false,
})

const emit = defineEmits<{
  /** A sidebar pane was selected. */
  'set-pane': [pane: SettingsPane]
  /** Persist the edited library knobs (carries the full edited copy). */
  'save-library': [settings: LibrarySettings]
  /** Persist the edited Suwayomi server config. */
  'save-suwayomi': [config: SuwayomiConfig]
  /** Add a new category by name. */
  'add-category': [name: string]
  /** Rename a category. */
  'rename-category': [payload: { id: string, name: string }]
  /** Move a category up (−1) or down (+1) in display order. */
  'reorder-category': [payload: { id: string, direction: ReorderDirection }]
  /** Delete a category; `targetId` is the reassign target ("" when it's empty). */
  'delete-category': [payload: { id: string, targetId: string }]
  /** Mark a category as the default landing for new series. */
  'set-default-category': [id: string]
  /** Start the embedded-engine upgrade flow. */
  'start-upgrade': []
  /** Install an available extension (by pkgName). */
  'install-extension': [id: string]
  /** Update an installed extension (by pkgName). */
  'update-extension': [id: string]
  /** Uninstall an installed extension (by pkgName). */
  'uninstall-extension': [id: string]
  /** Trigger a check-for-updates across installed extensions. */
  'check-updates': []
  /** Add an extension repository URL. */
  'add-repo': [url: string]
  /** Remove an extension repository (by id). */
  'remove-repo': [id: string]
  /** Move a repository up (−1) or down (+1) in the list. */
  'reorder-repo': [payload: { id: string, direction: ReorderDirection }]
}>()

const skeletons = Array.from({ length: 5 }, (_, i) => i)
</script>

<template>
  <div class="settings">
    <!-- Loading skeletons -->
    <div v-if="loading" class="settings__skeletons">
      <div v-for="n in skeletons" :key="n" class="skeleton-card" />
    </div>

    <div v-else class="settings__layout">
      <SettingsNav :active="activePane" @select="emit('set-pane', $event)" />

      <div class="pane">
        <LibraryPane
          v-if="activePane === 'library'"
          :library="library"
          :system="system"
          :save="librarySave"
          @save="emit('save-library', $event)"
        />

        <CategoriesPane
          v-else-if="activePane === 'categories'"
          :categories="categories"
          :category-action="categoryAction"
          @add-category="emit('add-category', $event)"
          @rename-category="emit('rename-category', $event)"
          @reorder-category="emit('reorder-category', $event)"
          @delete-category="emit('delete-category', $event)"
          @set-default-category="emit('set-default-category', $event)"
        />

        <EnginePane
          v-else-if="activePane === 'engine'"
          :engine="engine"
          :upgrade-steps="upgradeSteps"
          :upgrading="upgrading"
          @start-upgrade="emit('start-upgrade')"
        />

        <SuwayomiPane
          v-else-if="activePane === 'suwayomi'"
          :config="suwayomi"
          :save="suwayomiSave"
          @save="emit('save-suwayomi', $event)"
        />

        <ExtensionsPane
          v-else
          :extensions="extensions"
          :available-extensions="availableExtensions"
          :repos="repos"
          :extension-action="extensionAction"
          :repo-action="repoAction"
          :ext-check-interval="extCheckInterval"
          :checking-updates="checkingUpdates"
          @install-extension="emit('install-extension', $event)"
          @update-extension="emit('update-extension', $event)"
          @uninstall-extension="emit('uninstall-extension', $event)"
          @check-updates="emit('check-updates')"
          @add-repo="emit('add-repo', $event)"
          @remove-repo="emit('remove-repo', $event)"
          @reorder-repo="emit('reorder-repo', $event)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

.settings__layout {
  display: grid;
  grid-template-columns: 236px 1fr;
  gap: 22px;
  align-items: start;
  max-width: 1460px;
}

.pane {
  min-width: 0;
}

/* ---- Skeletons ------------------------------------------------------------ */
.settings__skeletons {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 1460px;
}

.skeleton-card {
  height: 120px;
  border-radius: var(--radius-2xl);
  background: var(--surface2);
  position: relative;
  overflow: hidden;
}

.skeleton-card::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: settings-shimmer 1.4s ease-in-out infinite;
}

@keyframes settings-shimmer {
  to { transform: translateX(100%); }
}
</style>
