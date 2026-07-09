<script setup lang="ts">
import SettingsNav from '../settings/SettingsNav.vue'
import LibraryPane from '../settings/LibraryPane.vue'
import CategoriesPane from '../settings/CategoriesPane.vue'
import EnginePane from '../settings/EnginePane.vue'
import SuwayomiPane from '../settings/SuwayomiPane.vue'
import ExtensionsPane from '../settings/ExtensionsPane.vue'
import SourceMetricsPane from '../settings/SourceMetricsPane.vue'
import SourcesSettingsPane from '../settings/SourcesSettingsPane.vue'
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
  SourceMetric,
  SourcesSettings,
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
 *   - sources     → SourcesSettingsPane (warm-up + circuit-breaker knobs +
 *                   the library-wide dedup-sweep trigger)
 *                   + SourceMetricsPane (per-source search metrics + Warm now),
 *                   stacked — mirrors how LibraryPane stacks its own two cards
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
  /** The runtime-editable warm-up + circuit-breaker knobs (2f, Sources pane). */
  sourcesSettings: SourcesSettings
  /** §16 state of the Sources-pane Save button. */
  sourcesSettingsSave?: SaveState
  /** Per-source search metrics (2f), slowest-first. */
  sourceMetrics?: SourceMetric[]
  /** Whether the source-metrics list is loading. */
  sourceMetricsPending?: boolean
  /** A source-metrics load failure, surfaced inline in the pane. */
  sourceMetricsError?: string | null
  /** Whether a manual warm-up pass is in flight. */
  warming?: boolean
  /** The last warm-up's success note (the warmed count). */
  warmMessage?: string | null
  /** The last warm-up's failure message. */
  warmError?: string | null
  /** True while the library-wide dedup sweep request is in flight. */
  dedupAllBusy?: boolean
  /** Started/success message from the last dedup sweep trigger. */
  dedupAllMessage?: string | null
  /** Error from the last dedup sweep trigger. */
  dedupAllError?: string | null
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
  sourcesSettingsSave: () => ({ status: 'idle' }),
  sourceMetrics: () => [],
  sourceMetricsPending: false,
  sourceMetricsError: null,
  warming: false,
  warmMessage: null,
  warmError: null,
  dedupAllBusy: false,
  dedupAllMessage: null,
  dedupAllError: null,
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
  /** The extension update-check cadence was changed by the user. */
  'update:ext-check-interval': [DurationValue]
  /** Persist the edited Sources-pane warm-up/circuit-breaker knobs. */
  'save-sources-settings': [settings: SourcesSettings]
  /** Trigger a manual warm-up pass across all sources. */
  'warm-now': []
  /** Trigger the library-wide duplicate-source dedup sweep. */
  'dedup-all': []
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
          v-else-if="activePane === 'extensions'"
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
          @update:ext-check-interval="emit('update:ext-check-interval', $event)"
        />

        <div v-else class="pane-stack">
          <SourcesSettingsPane
            :sources="sourcesSettings"
            :save="sourcesSettingsSave"
            :dedup-all-busy="dedupAllBusy"
            :dedup-all-message="dedupAllMessage"
            :dedup-all-error="dedupAllError"
            @save="emit('save-sources-settings', $event)"
            @dedup-all="emit('dedup-all')"
          />

          <SourceMetricsPane
            :metrics="sourceMetrics"
            :pending="sourceMetricsPending"
            :error="sourceMetricsError"
            :warming="warming"
            :warm-message="warmMessage"
            :warm-error="warmError"
            @warm-now="emit('warm-now')"
          />
        </div>
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

/* The Sources pane stacks two cards (settings + metrics) with the shared
   16px inter-card rhythm — same shape as LibraryPane's own pane-stack. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
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
