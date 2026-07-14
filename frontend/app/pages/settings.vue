<script setup lang="ts">
/**
 * Settings page — route "/settings".
 *
 * Assembles the 7-pane Settings screen from six composables:
 *   useSettings()         → library knobs + system info + saveLibrary
 *                           + extensionCheckInterval + saveExtensionCheckInterval
 *                           + autoUpdateTrack + saveAutoUpdateTrack (Phase 4
 *                           reading-triggered tracker-sync gate, Trackers pane)
 *                           + metadataAutoIdentify + saveMetadataAutoIdentify
 *                           (metadata-engine background auto-identify gate,
 *                           Library pane)
 *                           + sourcesSettings + saveSourcesSettings (warm-up +
 *                           circuit-breaker knobs, source-politeness spec)
 *   useCategories()       → settingsCategories + categoryAction + CRUD methods
 *   useSuwayomiSettings() → config + suwayomiSave + save
 *   useExtensions()       → extensions + repos + mutations (no longer the source of
 *                           extCheckInterval — that moved to useSettings)
 *   useSourceMetrics()    → sourceMetrics + pending/error + warmNow (Warm now)
 *   useLibraryMaintenance() → dedupAllBusy/Message/Error + dedupAllProviders
 *                           (library-wide duplicate-source dedup sweep)
 *   useTrackers()         → trackers + trackerAction (busyId/error) + misconfigured
 *                           + connect/loginCredentials/logout (Trackers pane)
 *
 * Prop wiring:
 *   :active-pane          — local activePane ref (default 'library')
 *   :library              — library from useSettings
 *   :system               — system from useSettings
 *   :library-save         — librarySave from useSettings
 *   :auto-identify        — metadataAutoIdentify from useSettings (Library pane)
 *   :auto-identify-busy   — computed, metadataAutoIdentifySave.status === 'saving'
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
 *   :sources-settings     — sourcesSettings from useSettings (shares its `pending`
 *                           with library/system — same GET /api/settings call, so
 *                           it stays in the global loading gate, unlike sourceMetrics)
 *   :sources-settings-save — sourcesSettingsSave from useSettings
 *   :source-metrics       — metrics from useSourceMetrics
 *   :source-metrics-pending — pending from useSourceMetrics (pane-owned, NOT in
 *                           the global loading gate so a warm refetch never
 *                           skeletons the whole screen)
 *   :source-metrics-error — error from useSourceMetrics
 *   :warming              — warming from useSourceMetrics
 *   :warm-message         — warmMessage from useSourceMetrics
 *   :warm-error           — warmError from useSourceMetrics
 *   :dedup-all-busy       — dedupAllBusy from useLibraryMaintenance
 *   :dedup-all-message    — dedupAllMessage from useLibraryMaintenance
 *   :dedup-all-error      — dedupAllError from useLibraryMaintenance
 *   :trackers             — trackers from useTrackers
 *   :tracker-action       — { busyId: actionBusyId, error: actionError } from useTrackers
 *   :misconfigured-tracker-ids — [...misconfigured] from useTrackers
 *   :tracker-redirect-url — trackerRedirectUrl (computed, this instance's OAuth callback URL)
 *   :trackers-pending     — pending from useTrackers (pane-owned, NOT in the
 *                           global loading gate — mirrors source-metrics-pending)
 *   :trackers-error       — error from useTrackers
 *   :auto-update-track    — autoUpdateTrack from useSettings (Phase 4)
 *   :auto-update-track-busy — computed, autoUpdateTrackSave.status === 'saving'
 *   :loading              — true while any primary dataset is still fetching
 *
 * Emit wiring:
 *   @set-pane                    → setPane (updates local activePane ref)
 *   @save-library                → saveLibrary
 *   @toggle-auto-identify        → saveMetadataAutoIdentify
 *   @save-suwayomi               → save
 *   @add-category                → addCategory
 *   @rename-category             → renameCategory
 *   @reorder-category            → reorderCategory
 *   @delete-category             → deleteCategory
 *   @set-default-category        → setDefaultCategory
 *   @start-upgrade               → no-op (engine deferred)
 *   @install-extension           → installExtension
 *   @update-extension            → updateExtension
 *   @uninstall-extension         → uninstallExtension
 *   @check-updates               → checkUpdates
 *   @add-repo                    → addRepo
 *   @remove-repo                 → removeRepo
 *   @reorder-repo                → reorderRepo
 *   @update:ext-check-interval   → saveExtensionCheckInterval
 *   @save-sources-settings       → saveSourcesSettings
 *   @warm-now                    → warmNow
 *   @dedup-all                   → dedupAllProviders
 *   @connect-tracker             → onConnectTracker (authUrl() → full-tab redirect;
 *                                   stashes the tracker id first — see trackerCallback.ts)
 *   @login-tracker-credentials   → onLoginTrackerCredentials
 *   @logout-tracker              → logoutTracker
 *   @toggle-auto-update-track    → saveAutoUpdateTrack
 *
 * OAuth flash handling: the callback route (`pages/auth/tracker/callback.vue`)
 * redirects back here with `?trackersFlash=connected` or
 * `?trackersFlash=error&trackersFlashMessage=...`. On mount, a present
 * `trackersFlash` opens the Trackers pane and — for the error case — writes
 * straight into `useTrackers`'s own `actionError` ref so it renders through the
 * SAME pane-level FormError a live connect failure would use (no separate flash
 * state to keep in sync). The query is stripped via `router.replace` so a page
 * refresh never replays the flash.
 */
import type { EngineInfo, SettingsPane } from '~/components/screens/settings.types'
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

const {
  metrics: sourceMetrics,
  pending: sourceMetricsPending,
  error: sourceMetricsError,
  warming,
  warmMessage,
  warmError,
  warmNow,
} = useSourceMetrics()

const {
  dedupAllBusy,
  dedupAllMessage,
  dedupAllError,
  dedupAllProviders,
} = useLibraryMaintenance()

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

/** Controlled pane selection — defaults to 'library'; updated by @set-pane. */
const activePane = ref<SettingsPane>('library')

/** Update the active pane; called by @set-pane from the Settings sidebar nav. */
function setPane(p: SettingsPane): void {
  activePane.value = p
}

// ---- OAuth callback flash (see the doc comment above) ----------------------
const route = useRoute()
const router = useRouter()
if (route.query.trackersFlash) {
  activePane.value = 'trackers'
  if (route.query.trackersFlash === 'error') {
    trackerActionError.value = typeof route.query.trackersFlashMessage === 'string'
      ? route.query.trackersFlashMessage
      : 'The tracker connection failed — try again.'
  }
  void router.replace({ path: '/settings', query: {} })
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
      :auto-identify="metadataAutoIdentify"
      :auto-identify-busy="autoIdentifyBusy"
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
      :sources-settings="sourcesSettings"
      :sources-settings-save="sourcesSettingsSave"
      :source-metrics="sourceMetrics"
      :source-metrics-pending="sourceMetricsPending"
      :source-metrics-error="sourceMetricsError"
      :warming="warming"
      :warm-message="warmMessage"
      :warm-error="warmError"
      :dedup-all-busy="dedupAllBusy"
      :dedup-all-message="dedupAllMessage"
      :dedup-all-error="dedupAllError"
      :trackers="trackers"
      :tracker-action="trackerAction"
      :misconfigured-tracker-ids="misconfiguredTrackerIds"
      :tracker-redirect-url="trackerRedirectUrl"
      :trackers-pending="trackersPending"
      :trackers-error="trackersError"
      :auto-update-track="autoUpdateTrack"
      :auto-update-track-busy="autoUpdateTrackBusy"
      :loading="loading"
      @set-pane="setPane"
      @save-library="saveLibrary"
      @toggle-auto-identify="saveMetadataAutoIdentify"
      @save-suwayomi="save"
      @add-category="addCategory"
      @rename-category="renameCategory"
      @reorder-category="reorderCategory"
      @delete-category="deleteCategory"
      @set-default-category="setDefaultCategory"
      @start-upgrade="() => {}"
      @install-extension="installExtension"
      @update-extension="updateExtension"
      @uninstall-extension="uninstallExtension"
      @check-updates="checkUpdates"
      @add-repo="addRepo"
      @remove-repo="removeRepo"
      @reorder-repo="reorderRepo"
      @update:ext-check-interval="saveExtensionCheckInterval"
      @save-sources-settings="saveSourcesSettings"
      @warm-now="warmNow"
      @dedup-all="dedupAllProviders"
      @connect-tracker="onConnectTracker"
      @login-tracker-credentials="onLoginTrackerCredentials"
      @logout-tracker="logoutTracker"
      @toggle-auto-update-track="saveAutoUpdateTrack"
    />
  </div>
</template>

<style scoped>
.page-settings {
  min-height: 100%;
}
</style>
