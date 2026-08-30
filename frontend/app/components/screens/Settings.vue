<script setup lang="ts">
import SettingsNav from '../settings/SettingsNav.vue'
import LibraryPane from '../settings/LibraryPane.vue'
import CategoriesPane from '../settings/CategoriesPane.vue'
import EnginePane from '../settings/EnginePane.vue'
import DownloadEnginePane from '../settings/DownloadEnginePane.vue'
import ExtensionsPane from '../settings/ExtensionsPane.vue'
import SourcesSettingsPane from '../settings/SourcesSettingsPane.vue'
import TrackersPane from '../settings/TrackersPane.vue'
import NotificationsPane from '../settings/NotificationsPane.vue'
import type {
  DurationValue,
  EngineInfo,
  Extension,
  FlareMode,
  FlareSolverrConfig,
  ImpersonateConfig,
  LibrarySettings,
  NetworkEndpoint,
  NetworkEndpointInput,
  NotificationPermissionState,
  Repo,
  ReorderDirection,
  RowActionState,
  SaveState,
  SettingsCategory,
  SettingsPane,
  SourceConfigurationRowKey,
  SourcesSettings,
  SystemInfo,
  TrackerActionState,
  TrackerStatus,
  UpgradeStep,
} from './settings.types'
import type { RedownloadFilter, RedownloadPreview } from '~/composables/useRedownload'
import type { SourceConfigurationAction } from '~/composables/useSourceEffectiveConfiguration'
import type { components } from '~/utils/api/schema.d.ts'

type SourceIdentity = components['schemas']['SourceIdentity']
type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']
type SourceEffectiveConfiguration = components['schemas']['SourceEffectiveConfiguration']

/**
 * Settings — the single-owner control panel. A thin container: a sticky sidebar
 * nav (SettingsNav) plus the one active pane, each pane extracted into its own
 * organism under `components/settings/`:
 *   - library     → LibraryPane + SourcesSettingsPane (metadata/system plus
 *                   owner-triggered maintenance and re-download actions)
 *   - categories  → CategoriesPane   (user-definable category CRUD)
 *   - download-engine → DownloadEnginePane (global scheduling/protection,
 *                   access, routing endpoints, and one source editor)
 *   - engine      → EnginePane       (read-only lifecycle diagnostics)
 *   - extensions  → ExtensionsPane    (installed / available / repositories)
 *   - trackers    → TrackersPane (connect/disconnect AniList/MAL/Kitsu/
 *                   MangaUpdates + the Phase 4 auto-update-track toggle;
 *                   per-series bind + the tracking-sheet edit live on Series
 *                   Detail's inline TrackersSection, QCAT-234)
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
  /** The `metadata.auto_identify` setting value (Library pane). */
  autoIdentify?: boolean
  /** True while the auto-identify toggle's own save is in flight. */
  autoIdentifyBusy?: boolean
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
  /** The Tsundoku-owned FlareSolverr config (QCAT-238, 2d). */
  flareSolverr: FlareSolverrConfig
  /** §16 state of the FlareSolverr Save button. */
  flareSolverrSave?: SaveState
  /** The Tsundoku-owned impersonate-gateway config (GAP-111, 2d). */
  impersonate: ImpersonateConfig
  /** §16 state of the impersonate Save button. */
  impersonateSave?: SaveState
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
  /** The runtime-editable warm-up + circuit-breaker knobs (Download engine). */
  sourcesSettings: SourcesSettings
  /** §16 state of the protection Save button. */
  sourcesSettingsSave?: SaveState
  /** Full installed catalog for the canonical source exception search. */
  sourceCatalog?: SourceIdentity[]
  /** Exception-first summary rows. */
  sourceSummaries?: SourceExceptionSummary[]
  /** Pane-local summary-list failure; unrelated global controls stay available. */
  sourceSummariesError?: string | null
  sourceCatalogPending?: boolean
  sourceCatalogLoaded?: boolean
  sourceCatalogError?: string | null
  /** Lossless source identity currently selected in the editor. */
  selectedSourceId?: string | null
  /** Last confirmed server-composed source configuration. */
  sourceConfiguration?: SourceEffectiveConfiguration | null
  sourceExceptionsPending?: boolean
  sourceConfigurationPending?: boolean
  sourceConfigurationError?: string | null
  /** External deep-link target in the source rail and editor. */
  highlightedSourceId?: string | null
  highlightedSetting?: SourceConfigurationRowKey | null
  sourceAction?: SourceConfigurationAction
  /** True while the library-wide dedup sweep request is in flight. */
  dedupAllBusy?: boolean
  /** Started/success message from the last dedup sweep trigger. */
  dedupAllMessage?: string | null
  /** Error from the last dedup sweep trigger. */
  dedupAllError?: string | null
  /** Series the last dedup sweep skipped because a merge was already running. */
  dedupAllSkippedBusy?: number
  /** The last bulk-re-download preview (Library pane), or null when none is loaded. */
  redownloadPreview?: RedownloadPreview | null
  /** True while the re-download preview request is in flight. */
  redownloadPreviewBusy?: boolean
  /** A failed-preview message for the re-download, or null. */
  redownloadPreviewError?: string | null
  /** True while the re-download apply request is in flight. */
  redownloadApplying?: boolean
  /** Success line from the last re-download apply, or null. */
  redownloadMessage?: string | null
  /** Failure line from the last re-download apply, or null. */
  redownloadError?: string | null
  /** Every registered tracker's connect status (2g, Trackers pane). */
  trackers?: TrackerStatus[]
  /** §16 state of the one in-flight tracker connect/login/logout action. */
  trackerAction?: TrackerActionState
  /** OAuth tracker ids known to be missing client-id/public-URL config. */
  misconfiguredTrackerIds?: number[]
  /** The callback URL to register with each OAuth tracker's app. */
  trackerRedirectUrl?: string
  /** Whether the tracker list is loading (kept OUT of the global `loading` gate — its own pane-local skeleton, mirrors sourceMetricsPending). */
  trackersPending?: boolean
  /** A tracker-list load failure, surfaced inline in the pane. */
  trackersError?: string | null
  /** The `trackers.auto_update_track` setting value (Phase 4). */
  autoUpdateTrack?: boolean
  /** True while the auto-update-track toggle's own save is in flight. */
  autoUpdateTrackBusy?: boolean
  /** This device's Web Push status (Notifications pane). */
  notifState?: NotificationPermissionState
  /** The server-side global notifications toggle (Notifications pane). */
  notifGlobalEnabled?: boolean
  /** True while the per-device enable/disable action is in flight. */
  notifBusy?: boolean
  /** A per-device notification action failure, surfaced inline. */
  notifError?: string | null
  /** True while the global notifications toggle save is in flight. */
  notifGlobalBusy?: boolean
  /** A global notifications toggle save failure, surfaced inline. */
  notifGlobalError?: string | null
  /** The defined network-egress endpoints (Download engine Routing). */
  networkEndpoints?: NetworkEndpoint[]
  /** §16 state of the endpoint save/delete mutation (busy row + error). */
  networkEndpointAction?: RowActionState
  /** Whether the endpoint list is loading (pane-owned, out of the global gate). */
  networkEndpointsPending?: boolean
  /** An endpoint-list load failure, surfaced inline. */
  networkEndpointsError?: string | null
  /** When true, the whole screen renders as skeletons. */
  loading?: boolean
}>(), {
  activePane: 'library',
  librarySave: () => ({ status: 'idle' }),
  autoIdentify: true,
  autoIdentifyBusy: false,
  categoryAction: () => ({ busyId: null }),
  upgradeSteps: () => [],
  upgrading: false,
  flareSolverrSave: () => ({ status: 'idle' }),
  impersonateSave: () => ({ status: 'idle' }),
  extensionAction: () => ({ busyId: null }),
  repoAction: () => ({ busyId: null }),
  checkingUpdates: false,
  sourcesSettingsSave: () => ({ status: 'idle' }),
  sourceCatalog: () => [],
  sourceSummaries: () => [],
  sourceSummariesError: null,
  sourceCatalogPending: false,
  sourceCatalogLoaded: false,
  sourceCatalogError: null,
  selectedSourceId: null,
  sourceConfiguration: null,
  sourceExceptionsPending: false,
  sourceConfigurationPending: false,
  sourceConfigurationError: null,
  highlightedSourceId: null,
  highlightedSetting: null,
  sourceAction: () => ({ sourceId: null, key: null, saving: false, error: null }),
  dedupAllBusy: false,
  dedupAllMessage: null,
  dedupAllError: null,
  dedupAllSkippedBusy: 0,
  redownloadPreview: null,
  redownloadPreviewBusy: false,
  redownloadPreviewError: null,
  redownloadApplying: false,
  redownloadMessage: null,
  redownloadError: null,
  trackers: () => [],
  trackerAction: () => ({ busyId: null }),
  misconfiguredTrackerIds: () => [],
  trackerRedirectUrl: '',
  trackersPending: false,
  trackersError: null,
  autoUpdateTrack: false,
  autoUpdateTrackBusy: false,
  notifState: 'default',
  notifGlobalEnabled: true,
  notifBusy: false,
  notifError: null,
  notifGlobalBusy: false,
  notifGlobalError: null,
  networkEndpoints: () => [],
  networkEndpointAction: () => ({ busyId: null }),
  networkEndpointsPending: false,
  networkEndpointsError: null,
  loading: false,
})

const emit = defineEmits<{
  /** A sidebar pane was selected. */
  'set-pane': [pane: SettingsPane]
  /** Persist the edited library knobs (carries the full edited copy). */
  'save-library': [settings: LibrarySettings]
  /** Persist the edited Tsundoku-owned FlareSolverr config. */
  'save-flaresolverr': [config: FlareSolverrConfig]
  /** Persist the edited Tsundoku-owned impersonate-gateway config. */
  'save-impersonate': [config: Pick<ImpersonateConfig, 'enabled' | 'url'>]
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
  /** Reinstall (roll back to) a held version of an installed extension. */
  'reinstall-extension': [payload: { id: string, versionCode: number }]
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
  /** Persist the edited Download-engine protection knobs. */
  'save-sources-settings': [settings: SourcesSettings]
  'select-source': [sourceId: string]
  'retry-source-summaries': []
  'retry-source-catalog': []
  'set-source-override': [sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean]
  'use-global-source-setting': [sourceId: string, key: SourceConfigurationRowKey]
  'set-source-binding': [payload: { sourceId: string, socksEndpointId: string | null, flareMode: FlareMode, flareEndpointId: string | null }]
  'clear-source-binding': [sourceId: string]
  /** Trigger the library-wide duplicate-source dedup sweep. */
  'dedup-all': []
  /** Load the bulk-re-download preview for this filter (reads only). */
  'redownload-preview': [filter: RedownloadFilter]
  /** Apply the bulk re-download (reachable only via its ConfirmModal). */
  'redownload': [filter: RedownloadFilter]
  /** The re-download filter changed — discard the loaded preview/outcome. */
  'redownload-reset': []
  /** The OAuth "Connect" button was pressed for a tracker id. */
  'connect-tracker': [trackerId: number]
  /** A credential sign-in form was submitted — carries the tracker id + pair. */
  'login-tracker-credentials': [payload: { trackerId: number, username: string, password: string }]
  /** The "Disconnect" button was pressed for a tracker id. */
  'logout-tracker': [trackerId: number]
  /** The auto-update-track toggle was flipped — carries the new value. */
  'toggle-auto-update-track': [value: boolean]
  /** The auto-identify toggle was flipped — carries the new value. */
  'toggle-auto-identify': [value: boolean]
  /** Enable Web Push on this device (Notifications pane). */
  'enable-notifications': []
  /** Disable Web Push on this device (Notifications pane). */
  'disable-notifications': []
  /** The global notifications toggle was flipped — carries the new value. */
  'set-notifications-global': [value: boolean]
  /** Create or update a network endpoint (id=null = create). */
  'save-endpoint': [input: NetworkEndpointInput]
  /** Remove a network endpoint by id. */
  'remove-endpoint': [id: string]
  /** Dismiss the lingering endpoint-action error banner. */
  'dismiss-endpoint-error': []
}>()

function forwardSourceOverride(sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean): void {
  emit('set-source-override', sourceId, key, value)
}

function forwardUseGlobal(sourceId: string, key: SourceConfigurationRowKey): void {
  emit('use-global-source-setting', sourceId, key)
}

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
        <div v-if="activePane === 'library'" class="pane-stack">
          <LibraryPane
            :system="system"
            :auto-identify="autoIdentify"
            :auto-identify-busy="autoIdentifyBusy"
            @toggle-auto-identify="emit('toggle-auto-identify', $event)"
          />
          <SourcesSettingsPane
            :dedup-all-busy="dedupAllBusy"
            :dedup-all-message="dedupAllMessage"
            :dedup-all-error="dedupAllError"
            :dedup-all-skipped-busy="dedupAllSkippedBusy"
            :redownload-preview="redownloadPreview"
            :redownload-preview-busy="redownloadPreviewBusy"
            :redownload-preview-error="redownloadPreviewError"
            :redownload-applying="redownloadApplying"
            :redownload-message="redownloadMessage"
            :redownload-error="redownloadError"
            @dedup-all="emit('dedup-all')"
            @redownload-preview="emit('redownload-preview', $event)"
            @redownload="emit('redownload', $event)"
            @redownload-reset="emit('redownload-reset')"
          />
        </div>

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

        <DownloadEnginePane
          v-else-if="activePane === 'download-engine'"
          :library="library"
          :library-save="librarySave"
          :sources="sourcesSettings"
          :sources-save="sourcesSettingsSave"
          :flare-solverr="flareSolverr"
          :flare-solverr-save="flareSolverrSave"
          :impersonate="impersonate"
          :impersonate-save="impersonateSave"
          :endpoints="networkEndpoints"
          :endpoint-action="networkEndpointAction"
          :endpoints-pending="networkEndpointsPending"
          :endpoints-error="networkEndpointsError"
          :source-catalog="sourceCatalog"
          :source-summaries="sourceSummaries"
          :source-summaries-error="sourceSummariesError"
          :source-catalog-pending="sourceCatalogPending"
          :source-catalog-loaded="sourceCatalogLoaded"
          :source-catalog-error="sourceCatalogError"
          :selected-source-id="selectedSourceId"
          :source-configuration="sourceConfiguration"
          :source-exceptions-pending="sourceExceptionsPending"
          :source-configuration-pending="sourceConfigurationPending"
          :source-configuration-error="sourceConfigurationError"
          :highlighted-source-id="highlightedSourceId"
          :highlighted-setting="highlightedSetting"
          :source-action="sourceAction"
          @save-library="emit('save-library', $event)"
          @save-sources="emit('save-sources-settings', $event)"
          @save-flaresolverr="emit('save-flaresolverr', $event)"
          @save-impersonate="emit('save-impersonate', $event)"
          @save-endpoint="emit('save-endpoint', $event)"
          @remove-endpoint="emit('remove-endpoint', $event)"
          @dismiss-endpoint-error="emit('dismiss-endpoint-error')"
          @select-source="emit('select-source', $event)"
          @retry-source-summaries="emit('retry-source-summaries')"
          @retry-source-catalog="emit('retry-source-catalog')"
          @set-source-override="forwardSourceOverride"
          @use-global-source-setting="forwardUseGlobal"
          @set-source-binding="emit('set-source-binding', $event)"
          @clear-source-binding="emit('clear-source-binding', $event)"
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
          @reinstall-extension="emit('reinstall-extension', $event)"
          @check-updates="emit('check-updates')"
          @add-repo="emit('add-repo', $event)"
          @remove-repo="emit('remove-repo', $event)"
          @reorder-repo="emit('reorder-repo', $event)"
          @update:ext-check-interval="emit('update:ext-check-interval', $event)"
        />

        <TrackersPane
          v-else-if="activePane === 'trackers'"
          :trackers="trackers"
          :tracker-action="trackerAction"
          :misconfigured-ids="misconfiguredTrackerIds"
          :redirect-url="trackerRedirectUrl"
          :pending="trackersPending"
          :error="trackersError"
          :auto-update-track="autoUpdateTrack"
          :auto-update-track-busy="autoUpdateTrackBusy"
          @connect="emit('connect-tracker', $event)"
          @login-credentials="emit('login-tracker-credentials', $event)"
          @logout="emit('logout-tracker', $event)"
          @toggle-auto-update-track="emit('toggle-auto-update-track', $event)"
        />

        <NotificationsPane
          v-else-if="activePane === 'notifications'"
          :state="notifState"
          :global-enabled="notifGlobalEnabled"
          :busy="notifBusy"
          :error="notifError"
          :global-busy="notifGlobalBusy"
          :global-error="notifGlobalError"
          @enable="emit('enable-notifications')"
          @disable="emit('disable-notifications')"
          @set-global="emit('set-notifications-global', $event)"
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

@media (max-width: 900px) {
  /* The fixed 236px sidebar + content grid has no room on a phone. Stack:
   * SettingsNav becomes a wrapping top row of pills (see its own
   * media query) and the pane takes the full width below it. */
  .settings {
    padding: 16px 16px 56px;
  }

  .settings__layout {
    grid-template-columns: 1fr;
    gap: 14px;
  }
}

/* Library composes its settings and maintenance cards with one shared rhythm. */
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
