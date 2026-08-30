<script setup lang="ts">
/**
 * Settings page — route `/settings` and composition root for the seven settings
 * destinations. Download engine owns global scheduling/protection, shared
 * access/routing, and the single per-source editor. Library retains metadata,
 * system facts, maintenance, and bulk re-download; engine lifecycle diagnostics
 * remain separate.
 *
 * Query state is durable and history-driven. A contextual link has the form
 * `/settings?pane=download-engine&source=<string-id>&setting=<row-key>`.
 * Parsing never coerces source identities, and the route watcher only consumes
 * history state; user selections write through `router.push`, preventing
 * replace/watch feedback loops while preserving reload, back, and forward.
 * Source summary/detail reads and failures stay pane-local and never participate
 * in the unrelated global Settings loading gate.
 *
 * Tracker OAuth callbacks are the one temporary query shape. They open Trackers,
 * publish any connection error through the existing tracker action state, then
 * replace the callback query with the canonical Trackers route so refresh cannot
 * replay the flash.
 */
import type {
  EngineInfo,
  FlareSolverrConfig,
  ImpersonateConfig,
  LibrarySettings,
  SettingsPane,
  SourceConfigurationRowKey,
  SourcesSettings,
} from '~/components/screens/settings.types'
import { buildSettingsQuery, parseSettingsHighlight } from '~/utils/settingsHighlight'
import { stashPendingTrackerId } from '~/utils/trackerCallback'

const {
  library,
  system,
  librarySave,
  extensionCheckInterval,
  saveExtensionCheckInterval,
  autoUpdateTrack,
  autoUpdateTrackSave,
  saveAutoUpdateTrack,
  metadataAutoIdentify,
  metadataAutoIdentifySave,
  saveMetadataAutoIdentify,
  sourcesSettings,
  sourcesSettingsSave,
  saveSourcesSettings,
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
  setDefaultCategory,
  deleteCategory,
} = useCategories()

const {
  config: flareSolverr,
  flareSolverrSave,
  pending: flareSolverrPending,
  save: saveFlareSolverr,
} = useFlareSolverrSettings()

const {
  config: impersonate,
  sources: sourceOptions,
  impersonateSave,
  pending: impersonatePending,
  catalogPending: sourceCatalogPending,
  catalogLoaded: sourceCatalogLoaded,
  catalogError: sourceCatalogError,
  save: saveImpersonate,
  refreshCatalog: refreshSourceCatalog,
} = useImpersonateSettings()

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
  reinstallExtension,
  checkUpdates,
  addRepo,
  removeRepo,
  reorderRepo,
} = useExtensions()

/** Reinstall a held version, unpacking the pane's {id, versionCode} payload. */
function onReinstallExtension({ id, versionCode }: { id: string, versionCode: number }): void {
  void reinstallExtension(id, versionCode)
}

const {
  dedupAllBusy,
  dedupAllMessage,
  dedupAllError,
  dedupAllSkippedBusy,
  dedupAllProviders,
} = useLibraryMaintenance()

// The previewed bulk re-download (Library pane). Kept in its own composable
// because it is a two-step read-then-write, unlike the fire-and-forget dedup
// sweep beside it.
const {
  preview: redownloadPreview,
  previewBusy: redownloadPreviewBusy,
  previewError: redownloadPreviewError,
  applying: redownloadApplying,
  applyMessage: redownloadMessage,
  applyError: redownloadError,
  loadPreview: loadRedownloadPreview,
  apply: applyRedownload,
  reset: resetRedownload,
} = useRedownload()

const {
  trackers,
  actionBusyId: trackerBusyId,
  actionError: trackerActionError,
  misconfigured: misconfiguredTrackers,
  pending: trackersPending,
  error: trackersError,
  authUrl,
  loginCredentials,
  logout: logoutTracker,
} = useTrackers()

const {
  state: notifState,
  globalEnabled: notifGlobalEnabled,
  busy: notifBusy,
  error: notifError,
  globalBusy: notifGlobalBusy,
  globalError: notifGlobalError,
  enable: enableNotifications,
  disable: disableNotifications,
  setGlobal: setNotificationsGlobal,
} = useNotifications()

const {
  endpoints: networkEndpoints,
  pending: networkEndpointsPending,
  error: networkEndpointsError,
  endpointAction: networkEndpointAction,
  saveEndpoint,
  removeEndpoint,
  clearActionError: clearEndpointActionError,
} = useNetworkEndpoints()

const {
  summaries: sourceSummaries,
  summariesError: sourceSummariesError,
  selected: sourceConfiguration,
  selectedSourceId,
  summariesPending: sourceExceptionsPending,
  selectedPending: sourceConfigurationPending,
  selectedError: sourceConfigurationError,
  action: sourceAction,
  loadSummaries: loadSourceSummaries,
  selectSource,
  refreshAfterGlobalChange: refreshSourceConfigurationAfterGlobalChange,
  setTransport: setSourceTransport,
  setThroughput: setSourceThroughput,
  setProxy: setSourceProxy,
  setBinding: setSourceBinding,
} = useSourceEffectiveConfiguration()

const sourceCatalog = computed(() => sourceOptions.value.map(source => ({
  sourceId: source.id,
  name: source.name,
  language: source.lang,
})))

function onSetSourceOverride(sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean): void {
  if (key === 'downloadConcurrency' && typeof value === 'number') {
    void setSourceThroughput(sourceId, key, { mode: 'override', value })
  }
  else if (key === 'imageRequestDelay' && typeof value === 'string') {
    void setSourceThroughput(sourceId, key, { mode: 'override', value })
  }
  else if (key === 'reuseBypassSession' && typeof value === 'boolean') {
    void setSourceTransport(sourceId, key, { mode: 'override', value })
  }
  else if (key === 'imageConnectionMode' && (value === 'fresh' || value === 'reuse')) {
    void setSourceTransport(sourceId, key, { mode: 'override', value })
  }
  else if (key === 'imageProxy' && typeof value === 'boolean') {
    void setSourceProxy(sourceId, value)
  }
}

function onUseGlobalSourceSetting(sourceId: string, key: SourceConfigurationRowKey): void {
  if (key === 'downloadConcurrency') {
    void setSourceThroughput(sourceId, key, { mode: 'inherit' })
  }
  else if (key === 'imageRequestDelay') {
    void setSourceThroughput(sourceId, key, { mode: 'inherit' })
  }
  else if (key === 'reuseBypassSession') {
    void setSourceTransport(sourceId, key, { mode: 'inherit' })
  }
  else if (key === 'imageConnectionMode') {
    void setSourceTransport(sourceId, key, { mode: 'inherit' })
  }
}

function onSetSourceBinding(payload: { sourceId: string, socksEndpointId: string | null, flareMode: 'none' | 'global' | 'endpoint', flareEndpointId: string | null }): void {
  const { sourceId, ...update } = payload
  void setSourceBinding(sourceId, update)
}

function onClearSourceBinding(sourceId: string): void {
  void setSourceBinding(sourceId, null)
}

async function onSaveLibrary(next: LibrarySettings): Promise<void> {
  await saveLibrary(next)
  if (librarySave.value.status === 'success') await refreshSourceConfigurationAfterGlobalChange()
}

async function onSaveSourcesSettings(next: SourcesSettings): Promise<void> {
  await saveSourcesSettings(next)
  if (sourcesSettingsSave.value.status === 'success') await refreshSourceConfigurationAfterGlobalChange()
}

async function onSaveFlareSolverr(next: FlareSolverrConfig): Promise<void> {
  await saveFlareSolverr(next)
  if (flareSolverrSave.value.status === 'success') await refreshSourceConfigurationAfterGlobalChange()
}

async function onSaveImpersonate(next: Pick<ImpersonateConfig, 'enabled' | 'url'>): Promise<void> {
  await saveImpersonate(next)
  if (impersonateSave.value.status === 'success') await refreshSourceConfigurationAfterGlobalChange()
}

/** { busyId, error } shape TrackersPane expects, derived from useTrackers' own refs. */
const trackerAction = computed(() => ({ busyId: trackerBusyId.value, error: trackerActionError.value ?? undefined }))
const misconfiguredTrackerIds = computed(() => [...misconfiguredTrackers.value])

/** This instance's OAuth callback URL — every tracker's app must register it. */
const trackerRedirectUrl = computed(() =>
  typeof window === 'undefined' ? '' : `${window.location.origin}/auth/tracker/callback`)

/** True while the auto-update-track toggle's own save is in flight (Phase 4). */
const autoUpdateTrackBusy = computed(() => autoUpdateTrackSave.value.status === 'saving')

/** True while the auto-identify toggle's own save is in flight (Library pane). */
const autoIdentifyBusy = computed(() => metadataAutoIdentifySave.value.status === 'saving')

/**
 * "Connect" was pressed for an OAuth tracker: build a fresh authorize URL,
 * stash the tracker id (the callback route has no other way to learn it — see
 * trackerCallback.ts), then hand the WHOLE TAB to the tracker's own site. A
 * misconfigured tracker's `authUrl()` call resolves null and never navigates —
 * the row instead flips to its "Not configured" shape (misconfiguredTrackerIds).
 */
async function onConnectTracker(trackerId: number): Promise<void> {
  const url = await authUrl(trackerId)
  if (!url) return
  stashPendingTrackerId(trackerId)
  window.location.href = url
}

/** A credential tracker's sign-in form was submitted. */
async function onLoginTrackerCredentials(payload: { trackerId: number, username: string, password: string }): Promise<void> {
  await loginCredentials(payload.trackerId, payload.username, payload.password)
}

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

const route = useRoute()
const router = useRouter()

const initialRouteState = parseSettingsHighlight(route.query)
const activePane = ref<SettingsPane>(initialRouteState.pane)
const highlightedSourceId = ref<string | null>(initialRouteState.source)
const highlightedSetting = ref<SourceConfigurationRowKey | null>(initialRouteState.setting)
let summariesLoaded = false
let trackerFlashHandled = false

function ensureSourceSummaries(): void {
  if (summariesLoaded) return
  summariesLoaded = true
  void loadSourceSummaries()
}

/** Route state is the sole back/forward authority; this watcher never writes it. */
watch(() => route.query, (query) => {
  if (!trackerFlashHandled && query.trackersFlash) {
    trackerFlashHandled = true
    activePane.value = 'trackers'
    highlightedSourceId.value = null
    highlightedSetting.value = null
    if (query.trackersFlash === 'error') {
      trackerActionError.value = typeof query.trackersFlashMessage === 'string'
        ? query.trackersFlashMessage
        : 'The tracker connection failed — try again.'
    }
    void router.replace({
      path: '/settings',
      query: buildSettingsQuery({ pane: 'trackers', source: null, setting: null }),
    })
    return
  }

  const state = parseSettingsHighlight(query)
  activePane.value = state.pane
  highlightedSourceId.value = state.source
  highlightedSetting.value = state.setting

  if (state.pane !== 'download-engine') return
  ensureSourceSummaries()
  if (state.source != null) void selectSource(state.source)
}, { deep: true, immediate: true })

/** Nav changes create history entries; route observation applies the state. */
function setPane(pane: SettingsPane): void {
  const source = pane === 'download-engine' ? selectedSourceId.value : null
  void router.push({
    path: '/settings',
    query: buildSettingsQuery({ pane, source, setting: null }),
  })
}

/** Source selection becomes durable URL state and is then loaded by the watcher. */
function setSelectedSource(source: string): void {
  void router.push({
    path: '/settings',
    query: buildSettingsQuery({ pane: 'download-engine', source, setting: null }),
  })
}

/**
 * Global loading skeleton while any primary dataset is still on its initial
 * fetch. Once all composables resolve, skeletons lift. The loading state is
 * intentionally broad (covers settings + categories + flaresolverr + extensions)
 * so the screen does not flash partial content on first render.
 */
const loading = computed(
  () => settingsPending.value || categoriesPending.value
    || flareSolverrPending.value || impersonatePending.value || extPending.value,
)
</script>

<template>
  <div class="page-settings">
    <Settings
      :active-pane="activePane"
      :library="library"
      :system="system"
      :library-save="librarySave"
      :auto-identify="metadataAutoIdentify"
      :auto-identify-busy="autoIdentifyBusy"
      :categories="settingsCategories"
      :category-action="categoryAction"
      :engine="ENGINE_PLACEHOLDER"
      :upgrade-steps="[]"
      :upgrading="false"
      :flare-solverr="flareSolverr"
      :flare-solverr-save="flareSolverrSave"
      :impersonate="impersonate"
      :impersonate-save="impersonateSave"
      :extensions="extensions"
      :available-extensions="availableExtensions"
      :repos="repos"
      :extension-action="extensionAction"
      :repo-action="repoAction"
      :ext-check-interval="extensionCheckInterval"
      :checking-updates="checkingUpdates"
      :sources-settings="sourcesSettings"
      :sources-settings-save="sourcesSettingsSave"
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
      :trackers="trackers"
      :tracker-action="trackerAction"
      :misconfigured-tracker-ids="misconfiguredTrackerIds"
      :tracker-redirect-url="trackerRedirectUrl"
      :trackers-pending="trackersPending"
      :trackers-error="trackersError"
      :auto-update-track="autoUpdateTrack"
      :auto-update-track-busy="autoUpdateTrackBusy"
      :notif-state="notifState"
      :notif-global-enabled="notifGlobalEnabled"
      :notif-busy="notifBusy"
      :notif-error="notifError"
      :notif-global-busy="notifGlobalBusy"
      :notif-global-error="notifGlobalError"
      :network-endpoints="networkEndpoints"
      :network-endpoint-action="networkEndpointAction"
      :network-endpoints-pending="networkEndpointsPending"
      :network-endpoints-error="networkEndpointsError"
      :loading="loading"
      @set-pane="setPane"
      @save-library="onSaveLibrary"
      @toggle-auto-identify="saveMetadataAutoIdentify"
      @save-flaresolverr="onSaveFlareSolverr"
      @save-impersonate="onSaveImpersonate"
      @add-category="addCategory"
      @rename-category="renameCategory"
      @reorder-category="reorderCategory"
      @delete-category="deleteCategory"
      @set-default-category="setDefaultCategory"
      @start-upgrade="() => {}"
      @install-extension="installExtension"
      @update-extension="updateExtension"
      @uninstall-extension="uninstallExtension"
      @reinstall-extension="onReinstallExtension"
      @check-updates="checkUpdates"
      @add-repo="addRepo"
      @remove-repo="removeRepo"
      @reorder-repo="reorderRepo"
      @update:ext-check-interval="saveExtensionCheckInterval"
      @save-sources-settings="onSaveSourcesSettings"
      @select-source="setSelectedSource"
      @retry-source-summaries="loadSourceSummaries"
      @retry-source-catalog="refreshSourceCatalog"
      @set-source-override="onSetSourceOverride"
      @use-global-source-setting="onUseGlobalSourceSetting"
      @set-source-binding="onSetSourceBinding"
      @clear-source-binding="onClearSourceBinding"
      @dedup-all="dedupAllProviders"
      @redownload-preview="loadRedownloadPreview"
      @redownload="applyRedownload"
      @redownload-reset="resetRedownload"
      @connect-tracker="onConnectTracker"
      @login-tracker-credentials="onLoginTrackerCredentials"
      @logout-tracker="logoutTracker"
      @toggle-auto-update-track="saveAutoUpdateTrack"
      @enable-notifications="enableNotifications"
      @disable-notifications="disableNotifications"
      @set-notifications-global="setNotificationsGlobal"
      @save-endpoint="saveEndpoint"
      @remove-endpoint="removeEndpoint"
      @dismiss-endpoint-error="clearEndpointActionError"
    />
  </div>
</template>

<style scoped>
.page-settings {
  min-height: 100%;
}
</style>
