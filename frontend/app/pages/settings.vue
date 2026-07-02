<script setup lang="ts">
/**
 * Settings page — route "/settings".
 *
 * Assembles the 5-pane Settings screen from four composables:
 *   useSettings()         → library knobs + system info + saveLibrary
 *                           + extensionCheckInterval + saveExtensionCheckInterval
 *   useCategories()       → settingsCategories + categoryAction + CRUD methods
 *   useSuwayomiSettings() → config + suwayomiSave + save
 *   useExtensions()       → extensions + repos + mutations (no longer the source of
 *                           extCheckInterval — that moved to useSettings)
 *
 * Prop wiring:
 *   :active-pane          — local activePane ref (default 'library')
 *   :library              — library from useSettings
 *   :system               — system from useSettings
 *   :library-save         — librarySave from useSettings
 *   :categories           — settingsCategories from useCategories
 *   :category-action      — categoryAction from useCategories
 *   :engine               — ENGINE_PLACEHOLDER (engine upgrade flow is deferred;
 *                           start-upgrade is no-op)
 *   :upgrade-steps        — [] static
 *   :upgrading            — false static
 *   :suwayomi             — config from useSuwayomiSettings
 *   :suwayomi-save        — suwayomiSave from useSuwayomiSettings
 *   :extensions           — extensions from useExtensions
 *   :available-extensions — availableExtensions from useExtensions
 *   :repos                — repos from useExtensions
 *   :extension-action     — extensionAction from useExtensions
 *   :repo-action          — repoAction from useExtensions
 *   :ext-check-interval   — extensionCheckInterval from useSettings (live tunable)
 *   :checking-updates     — checkingUpdates from useExtensions
 *   :loading              — true while any primary dataset is still fetching
 *
 * Emit wiring:
 *   @set-pane                    → setPane (updates local activePane ref)
 *   @save-library                → saveLibrary
 *   @save-suwayomi               → save
 *   @add-category                → addCategory
 *   @rename-category             → renameCategory
 *   @reorder-category            → reorderCategory
 *   @delete-category             → deleteCategory
 *   @set-default-category        → no-op (owner dropped this action)
 *   @start-upgrade               → no-op (engine deferred)
 *   @install-extension           → installExtension
 *   @update-extension            → updateExtension
 *   @uninstall-extension         → uninstallExtension
 *   @check-updates               → checkUpdates
 *   @add-repo                    → addRepo
 *   @remove-repo                 → removeRepo
 *   @reorder-repo                → reorderRepo
 *   @update:ext-check-interval   → saveExtensionCheckInterval
 */
import type { EngineInfo, SettingsPane } from '~/components/screens/settings.types'

const {
  library,
  system,
  librarySave,
  extensionCheckInterval,
  saveExtensionCheckInterval,
  pending: settingsPending,
  saveLibrary,
} = useSettings()

const {
  settingsCategories,
  categoryAction,
  pending: categoriesPending,
  addCategory,
  renameCategory,
  reorderCategory,
  deleteCategory,
} = useCategories()

const {
  config: suwayomi,
  suwayomiSave,
  pending: suwayomiPending,
  save,
} = useSuwayomiSettings()

const {
  extensions,
  availableExtensions,
  repos,
  extensionAction,
  repoAction,
  checkingUpdates,
  pending: extPending,
  installExtension,
  updateExtension,
  uninstallExtension,
  checkUpdates,
  addRepo,
  removeRepo,
  reorderRepo,
} = useExtensions()

/**
 * Engine upgrade flow is deferred.
 * This static constant satisfies the required EngineInfo prop so the Engine
 * pane renders its read-only status view without a real backend endpoint.
 * The @start-upgrade emit is wired to a no-op below.
 */
const ENGINE_PLACEHOLDER: EngineInfo = {
  mode: 'embedded',
  externalUrl: '',
  runningVersion: '',
  pinnedVersion: '',
  runtimeDir: '',
  javaPath: '',
  status: 'stopped',
  upgradeAvailable: false,
  availableVersion: '',
}

/** Controlled pane selection — defaults to 'library'; updated by @set-pane. */
const activePane = ref<SettingsPane>('library')

/** Update the active pane; called by @set-pane from the Settings sidebar nav. */
function setPane(p: SettingsPane): void {
  activePane.value = p
}

/**
 * Global loading skeleton while any primary dataset is still on its initial
 * fetch. Once all composables resolve, skeletons lift. The loading state is
 * intentionally broad (covers settings + categories + suwayomi + extensions)
 * so the screen does not flash partial content on first render.
 */
const loading = computed(
  () => settingsPending.value || categoriesPending.value || suwayomiPending.value || extPending.value,
)
</script>

<template>
  <div class="page-settings">
    <Settings
      :active-pane="activePane"
      :library="library"
      :system="system"
      :library-save="librarySave"
      :categories="settingsCategories"
      :category-action="categoryAction"
      :engine="ENGINE_PLACEHOLDER"
      :upgrade-steps="[]"
      :upgrading="false"
      :suwayomi="suwayomi"
      :suwayomi-save="suwayomiSave"
      :extensions="extensions"
      :available-extensions="availableExtensions"
      :repos="repos"
      :extension-action="extensionAction"
      :repo-action="repoAction"
      :ext-check-interval="extensionCheckInterval"
      :checking-updates="checkingUpdates"
      :loading="loading"
      @set-pane="setPane"
      @save-library="saveLibrary"
      @save-suwayomi="save"
      @add-category="addCategory"
      @rename-category="renameCategory"
      @reorder-category="reorderCategory"
      @delete-category="deleteCategory"
      @set-default-category="() => {}"
      @start-upgrade="() => {}"
      @install-extension="installExtension"
      @update-extension="updateExtension"
      @uninstall-extension="uninstallExtension"
      @check-updates="checkUpdates"
      @add-repo="addRepo"
      @remove-repo="removeRepo"
      @reorder-repo="reorderRepo"
      @update:ext-check-interval="saveExtensionCheckInterval"
    />
  </div>
</template>

<style scoped>
.page-settings {
  min-height: 100%;
}
</style>
