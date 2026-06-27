<script setup lang="ts">
import { computed, h, reactive, ref, watch } from 'vue'
import type { VNode } from 'vue'
import { ADD_ACTION_ID } from './settings.types'
import type {
  DurationValue,
  EngineInfo,
  Extension,
  ExtensionTab,
  FlareSolverrConfig,
  LibrarySettings,
  Repo,
  ReorderDirection,
  RowActionState,
  SaveState,
  SettingsCategory,
  SettingsPane,
  SocksProxyConfig,
  SuwayomiConfig,
  SystemInfo,
  UpgradeStep,
} from './settings.types'

/**
 * Settings — the single-owner control panel. ONE screen with an internal sticky
 * sidebar that switches between five panes:
 *   - library     → Schedules & Behavior (runtime-editable knobs) + read-only System
 *   - categories  → user-definable category CRUD (add / rename / reorder / delete)
 *   - engine      → read-only engine status + the embedded-engine upgrade stepper
 *   - suwayomi    → proxied Suwayomi server config (SOCKS proxy + FlareSolverr)
 *   - extensions  → installed / available / repositories management
 *
 * Presentation only: ALL state arrives via props and every mutation is emitted —
 * no fetching, routing, or stores. The editable forms (library, SOCKS, Flare)
 * keep a LOCAL editable copy seeded from props; Save emits that copy, and when
 * the parent reflects the persisted value back the copy re-seeds (§16 round-trip,
 * so the form rehydrates from the source of truth). Token-only colours → renders
 * correctly in both themes.
 *
 * PHASE B: the recurring atoms here — duration input (number + unit), read-only
 * locked row, toggle-gated card, save button + inline §16 result, reorderable
 * list row — are all candidates to atomise out of this SFC into the ui/ kit.
 */
const props = withDefaults(defineProps<{
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

// The padlock glyph fronting every read-only "set at deploy time" row. A local
// render-function component so the markup lives once instead of being repeated
// across the System / Engine locked rows (DRY) — used as `<LockIcon/>`.
const LockIcon = (): VNode =>
  h('svg', {
    'width': 15,
    'height': 15,
    'viewBox': '0 0 24 24',
    'fill': 'none',
    'stroke': 'currentColor',
    'stroke-width': 1.9,
    'stroke-linecap': 'round',
    'stroke-linejoin': 'round',
    'aria-hidden': 'true',
  }, [
    h('rect', { x: 4, y: 11, width: 16, height: 10, rx: 2 }),
    h('path', { d: 'M8 11V7a4 4 0 0 1 8 0v4' }),
  ])

// ---- Sidebar panes ----------------------------------------------------------
const PANES: { key: SettingsPane, label: string }[] = [
  { key: 'library', label: 'Schedules & Behavior' },
  { key: 'categories', label: 'Categories' },
  { key: 'engine', label: 'Engine' },
  { key: 'suwayomi', label: 'Server config' },
  { key: 'extensions', label: 'Sources & Extensions' },
]

const UNIT_OPTS: DurationValue['unit'][] = ['h', 'm', 's']

// ---- Local editable copies (seeded from props; re-seed on prop change) ------
// Deep-clone helpers keep the local copy fully detached from the props object.
const cloneLibrary = (l: LibrarySettings): LibrarySettings => ({
  refreshInterval: { ...l.refreshInterval },
  downloadInterval: { ...l.downloadInterval },
  retryBackoff: { ...l.retryBackoff },
  maxRetries: l.maxRetries,
  staleGraceDays: l.staleGraceDays,
  refreshConcurrency: l.refreshConcurrency,
})
const cloneSocks = (s: SocksProxyConfig): SocksProxyConfig => ({ ...s })
const cloneFlare = (f: FlareSolverrConfig): FlareSolverrConfig => ({
  ...f,
  timeout: { ...f.timeout },
  sessionTtl: { ...f.sessionTtl },
})

const lib = reactive(cloneLibrary(props.library))
const socks = reactive(cloneSocks(props.suwayomi.socks))
const flare = reactive(cloneFlare(props.suwayomi.flareSolverr))

// Re-seed the local copies whenever the source of truth changes (post-save
// rehydrate, §16): the persisted values flow back and dirty resets to false.
watch(() => props.library, (v) => Object.assign(lib, cloneLibrary(v)), { deep: true })
watch(() => props.suwayomi.socks, (v) => Object.assign(socks, cloneSocks(v)), { deep: true })
watch(() => props.suwayomi.flareSolverr, (v) => Object.assign(flare, cloneFlare(v)), { deep: true })

const libDirty = computed(() => JSON.stringify(lib) !== JSON.stringify(props.library))
const suwaDirty = computed(() =>
  JSON.stringify(socks) !== JSON.stringify(props.suwayomi.socks)
  || JSON.stringify(flare) !== JSON.stringify(props.suwayomi.flareSolverr),
)

// ---- Number/duration input writers (clamp to a non-negative integer) --------
const clampInt = (raw: string): number => Math.max(0, Number.parseInt(raw, 10) || 0)
const setDurValue = (d: DurationValue, e: Event): void => {
  d.value = clampInt((e.target as HTMLInputElement).value)
}
const setDurUnit = (d: DurationValue, e: Event): void => {
  d.unit = (e.target as HTMLSelectElement).value as DurationValue['unit']
}

const saveLibrary = (): void => {
  if (!libDirty.value || props.librarySave.status === 'saving') return
  emit('save-library', cloneLibrary(lib))
}
const saveSuwayomi = (): void => {
  if (!suwaDirty.value || props.suwayomiSave.status === 'saving') return
  emit('save-suwayomi', {
    database: props.suwayomi.database,
    socks: cloneSocks(socks),
    flareSolverr: cloneFlare(flare),
  })
}

const advancedOpen = ref(false)

// ---- Categories: derived rows + inline rename + confirm modal ---------------
interface CategoryRow extends SettingsCategory {
  canMoveUp: boolean
  canMoveDown: boolean
}
const categoryRows = computed<CategoryRow[]>(() =>
  props.categories.map((c, i, arr) => ({
    ...c,
    canMoveUp: i > 0,
    canMoveDown: i < arr.length - 1,
  })),
)

const NAME_RE = /^[A-Za-z0-9 _-]+$/
const newCategory = ref('')
const categoryError = ref('')
const renameId = ref<string | null>(null)
const renameVal = ref('')

/**
 * Confirm modal for a destructive/folder-moving action: a category rename or
 * delete (both physically move series folders) or an extension uninstall (brief
 * §2e requires a confirm). One shell serves all three.
 */
type ConfirmModal =
  | { kind: 'rename', id: string, name: string, from: string, count: number }
  | { kind: 'delete', id: string, name: string, count: number, targetId: string }
  | { kind: 'uninstall', id: string, name: string }
const confirmModal = ref<ConfirmModal | null>(null)

// The category-pane inline error = local client validation OR the parent's
// backend failure (categoryAction.error) — one line, whichever is set.
const categoryErrorMsg = computed(() => {
  if (categoryError.value) return categoryError.value
  return props.categoryAction.error ?? ''
})
// Per-row busy: the single category whose mutation the parent flagged in flight.
const categoryRowBusy = (id: string): boolean => props.categoryAction.busyId === id

const nameTaken = (name: string, exceptId?: string): boolean =>
  props.categories.some((c) => c.id !== exceptId && c.name.toLowerCase() === name.toLowerCase())

const addCategory = (): void => {
  const name = newCategory.value.trim()
  if (!name) return
  if (!NAME_RE.test(name)) {
    categoryError.value = 'Use letters, numbers, spaces, - or _'
    return
  }
  if (nameTaken(name)) {
    categoryError.value = 'A category with that name already exists'
    return
  }
  categoryError.value = ''
  emit('add-category', name)
  newCategory.value = ''
}

const startRename = (c: SettingsCategory): void => {
  renameId.value = c.id
  renameVal.value = c.name
  categoryError.value = ''
}
const cancelRename = (): void => {
  renameId.value = null
}
const saveRename = (c: SettingsCategory): void => {
  const to = renameVal.value.trim()
  if (!to || to === c.name) {
    renameId.value = null
    return
  }
  if (!NAME_RE.test(to)) {
    categoryError.value = 'Use letters, numbers, spaces, - or _'
    return
  }
  if (nameTaken(to, c.id)) {
    categoryError.value = 'A category with that name already exists'
    return
  }
  categoryError.value = ''
  renameId.value = null
  // A rename of a non-empty category physically moves its series folders — confirm.
  if (c.count > 0) {
    confirmModal.value = { kind: 'rename', id: c.id, name: to, from: c.name, count: c.count }
  }
  else {
    emit('rename-category', { id: c.id, name: to })
  }
}

const startDelete = (c: SettingsCategory): void => {
  const target = props.categories.find((x) => x.id !== c.id)
  if (!target) {
    categoryError.value = 'Cannot delete the last category'
    return
  }
  categoryError.value = ''
  // A non-empty category needs a reassign target before its folders can move.
  if (c.count > 0) {
    confirmModal.value = { kind: 'delete', id: c.id, name: c.name, count: c.count, targetId: target.id }
  }
  else {
    emit('delete-category', { id: c.id, targetId: '' })
  }
}

const targetOptions = computed(() => {
  const c = confirmModal.value
  return c ? props.categories.filter((x) => x.id !== c.id) : []
})

// The confirm modal narrowed to its delete variant (or null) — lets the
// template bind the reassign-target select without a union-narrowing dance.
const deleteModal = computed(() => {
  const c = confirmModal.value
  return c?.kind === 'delete' ? c : null
})
const setTarget = (e: Event): void => {
  if (confirmModal.value?.kind === 'delete') {
    confirmModal.value.targetId = (e.target as HTMLSelectElement).value
  }
}

// Whether the open confirm's target action is in flight — drives the confirm
// button's spinner + the close-on-completion watcher. A rename/delete reads the
// category action; an uninstall reads the extension action.
const confirmBusy = computed(() => {
  const c = confirmModal.value
  if (!c) return false
  return c.kind === 'uninstall'
    ? props.extensionAction.busyId === c.id
    : props.categoryAction.busyId === c.id
})

const confirmMove = (): void => {
  const c = confirmModal.value
  if (!c || confirmBusy.value) return
  if (c.kind === 'rename') emit('rename-category', { id: c.id, name: c.name })
  else if (c.kind === 'delete') emit('delete-category', { id: c.id, targetId: c.targetId })
  else emit('uninstall-extension', c.id)
  // With no async wiring (e.g. Storybook) close immediately; otherwise the
  // confirmBusy false-edge watcher below closes it once the action completes.
  if (!confirmBusy.value) confirmModal.value = null
}
const cancelConfirm = (): void => {
  confirmModal.value = null
}
watch(confirmBusy, (busy, prev) => {
  if (prev && !busy) confirmModal.value = null
})

const confirmTitle = computed(() => {
  const c = confirmModal.value
  if (!c) return ''
  if (c.kind === 'rename') return `Rename “${c.from}” → “${c.name}”?`
  if (c.kind === 'delete') return `Delete “${c.name}”?`
  return `Uninstall “${c.name}”?`
})
const confirmMessage = computed(() => {
  const c = confirmModal.value
  if (!c) return ''
  if (c.kind === 'uninstall') {
    return 'This removes the source extension from the engine. Downloaded chapters stay on disk.'
  }
  const plural = c.count > 1 ? 's' : ''
  return c.kind === 'rename'
    ? `This physically moves ${c.count} series folder${plural} on disk and rewrites their sidecars.`
    : `Choose where its ${c.count} series go — their folders move on disk. CBZ files are never deleted.`
})
// The confirm's primary button reads red for an uninstall (destructive).
const confirmIsDestructive = computed(() => confirmModal.value?.kind === 'uninstall')

// ---- Engine -----------------------------------------------------------------
const upgradeShown = computed(() => props.upgradeSteps.length > 0)
const startUpgrade = (): void => {
  if (props.upgrading) return
  emit('start-upgrade')
}

// ---- Extensions -------------------------------------------------------------
const extTab = ref<ExtensionTab>('installed')
const extTabs = computed(() => [
  { key: 'installed' as const, label: 'Installed', n: props.extensions.length },
  { key: 'available' as const, label: 'Available', n: props.availableExtensions.length },
  { key: 'repos' as const, label: 'Repositories', n: props.repos.length },
])

// Per-row busy flags (the parent flags the single in-flight pkgName / repo id).
const extensionRowBusy = (id: string): boolean => props.extensionAction.busyId === id
const repoRowBusy = (id: string): boolean => props.repoAction.busyId === id
const repoAddBusy = computed(() => props.repoAction.busyId === ADD_ACTION_ID)

// Uninstall is destructive (brief §2e) — route it through the confirm modal
// instead of emitting straight from the row button.
const startUninstall = (e: Extension): void => {
  confirmModal.value = { kind: 'uninstall', id: e.id, name: e.name }
}

interface RepoRow extends Repo {
  canMoveUp: boolean
  canMoveDown: boolean
}
const repoRows = computed<RepoRow[]>(() =>
  props.repos.map((r, i, arr) => ({
    ...r,
    canMoveUp: i > 0,
    canMoveDown: i < arr.length - 1,
  })),
)

const newRepo = ref('')
const repoError = ref('')
// One inline repo error = local URL validation OR the parent's backend failure.
const repoErrorMsg = computed(() => {
  if (repoError.value) return repoError.value
  return props.repoAction.error ?? ''
})
const addRepo = (): void => {
  const url = newRepo.value.trim()
  if (!url) return
  if (!/^https?:\/\//.test(url)) {
    repoError.value = 'Enter a valid URL (https://…)'
    return
  }
  repoError.value = ''
  emit('add-repo', url)
  newRepo.value = ''
}

// A deterministic accent hue for an extension's square avatar (id-derived).
const provHue = (id: string): number => {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) % 360
  return h
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
      <!-- Sidebar nav -->
      <nav class="nav">
        <button
          v-for="p in PANES"
          :key="p.key"
          type="button"
          class="nav__item"
          :class="{ 'nav__item--active': activePane === p.key }"
          @click="emit('set-pane', p.key)"
        >
          {{ p.label }}
        </button>
      </nav>

      <div class="pane">
        <!-- ===================== 2a · LIBRARY ===================== -->
        <template v-if="activePane === 'library'">
          <section class="card">
            <h2 class="card__title">Schedules &amp; Behavior</h2>
            <p class="card__sub">Runtime-editable timing. The job schedulers re-read these on the next tick.</p>

            <div class="srow">
              <div class="srow__label">
                <div class="srow__name">Refresh interval</div>
                <div class="srow__hint">How often to poll titles for new chapters</div>
              </div>
              <div class="dur">
                <input class="input input--num" type="number" min="0" :value="lib.refreshInterval.value" @input="setDurValue(lib.refreshInterval, $event)">
                <select class="input input--unit" :value="lib.refreshInterval.unit" @change="setDurUnit(lib.refreshInterval, $event)">
                  <option v-for="u in UNIT_OPTS" :key="u" :value="u">{{ u }}</option>
                </select>
              </div>
            </div>

            <div class="srow">
              <div class="srow__label">
                <div class="srow__name">Download interval</div>
                <div class="srow__hint">Queue-drain &amp; upgrade-swap cadence</div>
              </div>
              <div class="dur">
                <input class="input input--num" type="number" min="0" :value="lib.downloadInterval.value" @input="setDurValue(lib.downloadInterval, $event)">
                <select class="input input--unit" :value="lib.downloadInterval.unit" @change="setDurUnit(lib.downloadInterval, $event)">
                  <option v-for="u in UNIT_OPTS" :key="u" :value="u">{{ u }}</option>
                </select>
              </div>
            </div>

            <div class="srow">
              <div class="srow__label">
                <div class="srow__name">Chapter retry backoff</div>
                <div class="srow__hint">Wait before retrying a failed chapter</div>
              </div>
              <div class="dur">
                <input class="input input--num" type="number" min="0" :value="lib.retryBackoff.value" @input="setDurValue(lib.retryBackoff, $event)">
                <select class="input input--unit" :value="lib.retryBackoff.unit" @change="setDurUnit(lib.retryBackoff, $event)">
                  <option v-for="u in UNIT_OPTS" :key="u" :value="u">{{ u }}</option>
                </select>
              </div>
            </div>

            <div class="srow">
              <div class="srow__label">
                <div class="srow__name">Chapter max retries</div>
                <div class="srow__hint">Attempts before a chapter is permanently failed</div>
              </div>
              <input class="input input--num input--wide" type="number" min="0" :value="lib.maxRetries" @input="lib.maxRetries = clampInt(($event.target as HTMLInputElement).value)">
            </div>

            <div class="srow">
              <div class="srow__label">
                <div class="srow__name">Stale-grace days</div>
                <div class="srow__hint">Health threshold before a source counts as stale</div>
              </div>
              <input class="input input--num input--wide" type="number" min="0" :value="lib.staleGraceDays" @input="lib.staleGraceDays = clampInt(($event.target as HTMLInputElement).value)">
            </div>

            <div class="advanced">
              <button type="button" class="advanced__toggle" @click="advancedOpen = !advancedOpen">
                <svg class="advanced__chev" :class="{ 'advanced__chev--open': advancedOpen }" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 18l6-6-6-6" /></svg>
                Advanced
              </button>
              <div v-if="advancedOpen" class="srow srow--advanced">
                <div class="srow__label">
                  <div class="srow__name">Refresh concurrency</div>
                  <div class="srow__hint">Parallel source fetches — be gentle on sources</div>
                </div>
                <input class="input input--num input--wide" type="number" min="0" :value="lib.refreshConcurrency" @input="lib.refreshConcurrency = clampInt(($event.target as HTMLInputElement).value)">
              </div>
            </div>

            <div class="card__foot">
              <span v-if="librarySave.status === 'success'" class="save-result save-result--ok">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
                Saved
              </span>
              <span v-else-if="librarySave.status === 'error'" class="save-result save-result--err">{{ librarySave.message || 'Save failed' }}</span>
              <button type="button" class="primary-btn" :disabled="!libDirty || librarySave.status === 'saving'" @click="saveLibrary">
                <span v-if="librarySave.status === 'saving'" class="spinner" aria-hidden="true" />
                Save changes
              </button>
            </div>
          </section>

          <section class="card">
            <h2 class="card__title">System</h2>
            <p class="card__sub">Set at deploy time via environment variables — read-only here.</p>
            <div class="lrow">
              <span class="lrow__label"><LockIcon class="lrow__lock" />Storage folder</span>
              <span class="lrow__val">{{ system.storageFolder }}</span>
            </div>
            <div class="lrow">
              <span class="lrow__label"><LockIcon class="lrow__lock" />Server port</span>
              <span class="lrow__val">{{ system.serverPort }}</span>
            </div>
            <div class="lrow">
              <span class="lrow__label"><LockIcon class="lrow__lock" />Database</span>
              <span class="lrow__val">{{ system.database }}</span>
            </div>
          </section>
        </template>

        <!-- ===================== 2b · CATEGORIES ===================== -->
        <template v-else-if="activePane === 'categories'">
          <section class="card">
            <h2 class="card__title">Categories</h2>
            <p class="card__sub">User-defined. Renaming or deleting moves series folders on disk — CBZ files are never deleted.</p>

            <div v-for="c in categoryRows" :key="c.id" class="cat-row" :class="{ 'cat-row--busy': categoryRowBusy(c.id) }">
              <div class="reorder">
                <button type="button" class="reorder__btn" :disabled="!c.canMoveUp || categoryRowBusy(c.id)" aria-label="Move up" @click="emit('reorder-category', { id: c.id, direction: -1 })">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 15l-6-6-6 6" /></svg>
                </button>
                <button type="button" class="reorder__btn" :disabled="!c.canMoveDown || categoryRowBusy(c.id)" aria-label="Move down" @click="emit('reorder-category', { id: c.id, direction: 1 })">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
                </button>
              </div>

              <!-- Inline rename -->
              <div v-if="renameId === c.id" class="cat-edit">
                <input
                  v-model="renameVal"
                  class="input cat-edit__input"
                  @keydown.enter="saveRename(c)"
                  @keydown.esc="cancelRename"
                >
                <button type="button" class="mini-btn mini-btn--accent" @click="saveRename(c)">Save</button>
                <button type="button" class="mini-btn" @click="cancelRename">Cancel</button>
              </div>

              <!-- Display -->
              <div v-else class="cat-main">
                <span class="chip">{{ c.name }}</span>
                <span class="cat-count">{{ c.count }} series</span>
                <span v-if="c.isDefault" class="pill">DEFAULT</span>
                <span v-if="categoryRowBusy(c.id)" class="row-busy"><span class="spinner spinner--dark" aria-hidden="true" />Working…</span>
                <div class="cat-actions">
                  <button v-if="!c.protected && !c.isDefault" type="button" class="text-btn" :disabled="categoryRowBusy(c.id)" @click="emit('set-default-category', c.id)">Set default</button>
                  <button v-if="!c.protected" type="button" class="icon-btn" aria-label="Rename" :disabled="categoryRowBusy(c.id)" @click="startRename(c)">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>
                  </button>
                  <button v-if="!c.protected" type="button" class="icon-btn icon-btn--danger" aria-label="Delete" :disabled="categoryRowBusy(c.id)" @click="startDelete(c)">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /></svg>
                  </button>
                </div>
              </div>
            </div>

            <p v-if="categoryErrorMsg" class="form-error">{{ categoryErrorMsg }}</p>
            <div class="add-row">
              <input v-model="newCategory" class="input add-row__input" placeholder="New category name…" :disabled="categoryRowBusy('__add__')" @keydown.enter="addCategory">
              <button type="button" class="primary-btn" :disabled="categoryRowBusy('__add__')" @click="addCategory">
                <span v-if="categoryRowBusy('__add__')" class="spinner" aria-hidden="true" />
                <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
                Add
              </button>
            </div>
          </section>
        </template>

        <!-- ===================== 2c · ENGINE ===================== -->
        <template v-else-if="activePane === 'engine'">
          <section class="card">
            <div class="card__head-row">
              <h2 class="card__title">Suwayomi engine</h2>
              <span class="mode-badge">{{ engine.mode === 'embedded' ? 'Embedded' : 'External' }}</span>
            </div>

            <template v-if="engine.mode === 'external'">
              <p class="card__sub">Pointing at an external instance — Tsundoku does not manage its lifecycle.</p>
              <div class="lrow">
                <span class="lrow__label"><LockIcon class="lrow__lock" />External URL</span>
                <span class="lrow__val">{{ engine.externalUrl }}</span>
              </div>
            </template>

            <template v-else>
              <p class="card__sub">Tsundoku provisions and runs its own engine JAR.</p>
              <div class="srow">
                <div class="srow__label">
                  <div class="srow__name">Running version</div>
                  <div class="srow__hint">pinned target {{ engine.pinnedVersion }}</div>
                </div>
                <div class="status-line">
                  <span class="status-dot" />
                  <span class="status-text">{{ engine.status }}</span>
                  <span class="mono">{{ engine.runningVersion }}</span>
                </div>
              </div>
              <div class="lrow">
                <span class="lrow__label"><LockIcon class="lrow__lock" />Runtime dir</span>
                <span class="lrow__val">{{ engine.runtimeDir }}</span>
              </div>
              <div class="lrow">
                <span class="lrow__label"><LockIcon class="lrow__lock" />Java path</span>
                <span class="lrow__val">{{ engine.javaPath }}</span>
              </div>

              <div class="engine-upgrade">
                <div v-if="!engine.upgradeAvailable" class="uptodate">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
                  Up to date
                </div>
                <div v-else class="upgrade-avail">
                  <div class="upgrade-avail__text">A newer pinned version <b>{{ engine.availableVersion }}</b> is available.</div>
                  <button type="button" class="primary-btn" :disabled="upgrading" @click="startUpgrade">
                    <span v-if="upgrading" class="spinner" aria-hidden="true" />
                    Upgrade to {{ engine.availableVersion }}
                  </button>
                </div>

                <div v-if="upgradeShown" class="stepper">
                  <div v-for="(st, i) in upgradeSteps" :key="st.label" class="step">
                    <span class="step__dot" :class="`step__dot--${st.status}`">
                      <svg v-if="st.status === 'done'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
                      <span v-else-if="st.status === 'active'" class="spinner spinner--step" aria-hidden="true" />
                      <span v-else class="step__num">{{ i + 1 }}</span>
                    </span>
                    <span class="step__label">{{ st.label }}</span>
                  </div>
                </div>
              </div>
            </template>
          </section>
        </template>

        <!-- ===================== 2d · SUWAYOMI SERVER CONFIG ===================== -->
        <template v-else-if="activePane === 'suwayomi'">
          <section class="card">
            <h2 class="card__title">Database</h2>
            <p class="card__sub">The engine's DB backend — a deploy concern, read-only here.</p>
            <div class="lrow"><span class="lrow__label-plain">Type</span><span class="lrow__val">{{ suwayomi.database.type }}</span></div>
            <div class="lrow"><span class="lrow__label-plain">URL</span><span class="lrow__val">{{ suwayomi.database.url }}</span></div>
            <div class="lrow"><span class="lrow__label-plain">Username</span><span class="lrow__val">{{ suwayomi.database.username }}</span></div>
            <div class="lrow"><span class="lrow__label-plain">Password</span><span class="lrow__val lrow__val--muted">••••••••</span></div>
          </section>

          <section class="card">
            <div class="card__head-row">
              <div>
                <h2 class="card__title">SOCKS proxy</h2>
                <p class="card__sub card__sub--tight">Route source traffic through a SOCKS proxy</p>
              </div>
              <button type="button" class="switch" :class="{ 'switch--on': socks.enabled }" role="switch" :aria-checked="socks.enabled" @click="socks.enabled = !socks.enabled">
                <span class="switch__knob" />
              </button>
            </div>
            <div v-if="socks.enabled" class="field-grid">
              <label class="field"><span class="field__label">Version</span><input v-model="socks.version" class="input"></label>
              <label class="field"><span class="field__label">Host</span><input v-model="socks.host" class="input" placeholder="127.0.0.1"></label>
              <label class="field"><span class="field__label">Port</span><input v-model="socks.port" class="input"></label>
              <label class="field"><span class="field__label">Username</span><input v-model="socks.username" class="input"></label>
              <label class="field field--full"><span class="field__label">Password</span><input v-model="socks.password" type="password" class="input" placeholder="••••••••"></label>
            </div>
          </section>

          <section class="card">
            <div class="card__head-row">
              <div>
                <h2 class="card__title">Cloudflare bypass (FlareSolverr)</h2>
                <p class="card__sub card__sub--tight">Solve Cloudflare challenges for protected sources</p>
              </div>
              <button type="button" class="switch" :class="{ 'switch--on': flare.enabled }" role="switch" :aria-checked="flare.enabled" @click="flare.enabled = !flare.enabled">
                <span class="switch__knob" />
              </button>
            </div>
            <div v-if="flare.enabled" class="flare-body">
              <label class="field field--block"><span class="field__label">Server URL</span><input v-model="flare.url" class="input"></label>
              <div class="field-grid">
                <div class="field">
                  <span class="field__label">Request timeout</span>
                  <div class="dur">
                    <input class="input input--num" type="number" min="0" :value="flare.timeout.value" @input="setDurValue(flare.timeout, $event)">
                    <select class="input input--unit" :value="flare.timeout.unit" @change="setDurUnit(flare.timeout, $event)">
                      <option v-for="u in UNIT_OPTS" :key="u" :value="u">{{ u }}</option>
                    </select>
                  </div>
                </div>
                <label class="field"><span class="field__label">Session name</span><input v-model="flare.session" class="input"></label>
                <div class="field">
                  <span class="field__label">Session TTL</span>
                  <div class="dur">
                    <input class="input input--num" type="number" min="0" :value="flare.sessionTtl.value" @input="setDurValue(flare.sessionTtl, $event)">
                    <select class="input input--unit" :value="flare.sessionTtl.unit" @change="setDurUnit(flare.sessionTtl, $event)">
                      <option v-for="u in UNIT_OPTS" :key="u" :value="u">{{ u }}</option>
                    </select>
                  </div>
                </div>
                <div class="field field--inline">
                  <button type="button" class="switch" :class="{ 'switch--on': flare.fallback }" role="switch" :aria-checked="flare.fallback" @click="flare.fallback = !flare.fallback">
                    <span class="switch__knob" />
                  </button>
                  <span class="field__inline-label">Response fallback</span>
                </div>
              </div>
            </div>
          </section>

          <div class="suwa-foot">
            <span v-if="suwayomiSave.status === 'success'" class="save-result save-result--ok">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
              Saved
            </span>
            <span v-else-if="suwayomiSave.status === 'error'" class="save-result save-result--err">{{ suwayomiSave.message || 'Save failed' }}</span>
            <button type="button" class="primary-btn" :disabled="!suwaDirty || suwayomiSave.status === 'saving'" @click="saveSuwayomi">
              <span v-if="suwayomiSave.status === 'saving'" class="spinner" aria-hidden="true" />
              Save engine settings
            </button>
          </div>
        </template>

        <!-- ===================== 2e · SOURCES & EXTENSIONS ===================== -->
        <template v-else>
          <div class="ext-tabs">
            <button
              v-for="t in extTabs"
              :key="t.key"
              type="button"
              class="tab"
              :class="{ 'tab--active': extTab === t.key }"
              @click="extTab = t.key"
            >
              {{ t.label }}
              <span class="tab__count" :class="{ 'tab__count--active': extTab === t.key }">{{ t.n }}</span>
            </button>
          </div>

          <!-- A failed extension mutation is surfaced inline for the whole pane. -->
          <p v-if="extensionAction.error" class="form-error form-error--pane">{{ extensionAction.error }}</p>

          <!-- Installed -->
          <template v-if="extTab === 'installed'">
            <div class="ext-actions">
              <button type="button" class="mini-btn" :disabled="checkingUpdates" @click="emit('check-updates')">
                <span v-if="checkingUpdates" class="spinner spinner--dark" aria-hidden="true" />
                Check for updates
              </button>
            </div>
            <div class="ext-grid">
              <div v-for="e in extensions" :key="e.id" class="ext-card" :class="{ 'ext-card--busy': extensionRowBusy(e.id) }">
                <span class="ext-card__avatar" :style="{ background: `hsl(${provHue(e.id)} 55% 30%)` }" />
                <div class="ext-card__body">
                  <div class="ext-card__titleline">
                    <span class="ext-card__name">{{ e.name }}</span>
                    <span class="ext-card__lang">{{ e.lang.toUpperCase() }}</span>
                    <span v-if="e.hasUpdate" class="update-badge">UPDATE</span>
                  </div>
                  <div class="ext-card__version">v{{ e.version }}</div>
                </div>
                <button v-if="e.hasUpdate" type="button" class="solid-btn" :disabled="extensionRowBusy(e.id)" @click="emit('update-extension', e.id)">
                  <span v-if="extensionRowBusy(e.id)" class="spinner" aria-hidden="true" />
                  Update
                </button>
                <button type="button" class="ghost-btn ghost-btn--danger" :disabled="extensionRowBusy(e.id)" @click="startUninstall(e)">
                  <span v-if="extensionRowBusy(e.id)" class="spinner spinner--dark" aria-hidden="true" />
                  Uninstall
                </button>
              </div>
            </div>
          </template>

          <!-- Available -->
          <template v-else-if="extTab === 'available'">
            <div class="ext-grid">
              <div v-for="e in availableExtensions" :key="e.id" class="ext-card" :class="{ 'ext-card--busy': extensionRowBusy(e.id) }">
                <span class="ext-card__avatar" :style="{ background: `hsl(${provHue(e.id)} 55% 30%)` }" />
                <div class="ext-card__body">
                  <div class="ext-card__titleline">
                    <span class="ext-card__name">{{ e.name }}</span>
                    <span class="ext-card__lang">{{ e.lang.toUpperCase() }}</span>
                  </div>
                  <div class="ext-card__version">v{{ e.version }}</div>
                </div>
                <button type="button" class="mini-btn" :disabled="extensionRowBusy(e.id)" @click="emit('install-extension', e.id)">
                  <span v-if="extensionRowBusy(e.id)" class="spinner spinner--dark" aria-hidden="true" />
                  <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
                  Install
                </button>
              </div>
            </div>
          </template>

          <!-- Repositories -->
          <template v-else>
            <div v-for="r in repoRows" :key="r.id" class="repo-row" :class="{ 'repo-row--busy': repoRowBusy(r.id) }">
              <div class="reorder">
                <button type="button" class="reorder__btn" :disabled="!r.canMoveUp || repoRowBusy(r.id)" aria-label="Move up" @click="emit('reorder-repo', { id: r.id, direction: -1 })">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 15l-6-6-6 6" /></svg>
                </button>
                <button type="button" class="reorder__btn" :disabled="!r.canMoveDown || repoRowBusy(r.id)" aria-label="Move down" @click="emit('reorder-repo', { id: r.id, direction: 1 })">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
                </button>
              </div>
              <span class="repo-row__url">{{ r.url }}</span>
              <span v-if="r.isDefault" class="pill">DEFAULT</span>
              <span v-if="repoRowBusy(r.id)" class="spinner spinner--dark" aria-hidden="true" />
              <button type="button" class="icon-btn icon-btn--danger" aria-label="Remove" :disabled="repoRowBusy(r.id)" @click="emit('remove-repo', r.id)">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /></svg>
              </button>
            </div>

            <p v-if="repoErrorMsg" class="form-error">{{ repoErrorMsg }}</p>
            <div class="add-row">
              <input v-model="newRepo" class="input add-row__input add-row__input--mono" placeholder="https://…/index.min.json" :disabled="repoAddBusy" @keydown.enter="addRepo">
              <button type="button" class="primary-btn" :disabled="repoAddBusy" @click="addRepo">
                <span v-if="repoAddBusy" class="spinner" aria-hidden="true" />
                Add repo
              </button>
            </div>

            <div class="srow srow--bordered">
              <div class="srow__label">
                <div class="srow__name">Extension update check</div>
                <div class="srow__hint">How often to auto-check for extension updates</div>
              </div>
              <div class="dur">
                <input class="input input--num" type="number" min="0" :value="extCheckInterval.value" disabled>
                <select class="input input--unit" :value="extCheckInterval.unit" disabled>
                  <option v-for="u in UNIT_OPTS" :key="u" :value="u">{{ u }}</option>
                </select>
              </div>
            </div>
          </template>
        </template>
      </div>
    </div>

    <!-- Confirm modal: category rename/delete (folder move) or extension uninstall. -->
    <div v-if="confirmModal" class="modal">
      <div class="modal__card">
        <div class="modal__title">{{ confirmTitle }}</div>
        <div class="modal__text">{{ confirmMessage }}</div>
        <div v-if="deleteModal" class="modal__field">
          <label class="field__label">Move series to</label>
          <select class="input input--select" :value="deleteModal.targetId" @change="setTarget">
            <option v-for="o in targetOptions" :key="o.id" :value="o.id">{{ o.name }}</option>
          </select>
        </div>
        <div class="modal__actions">
          <button type="button" class="ghost-btn" :disabled="confirmBusy" @click="cancelConfirm">Cancel</button>
          <button type="button" class="primary-btn" :class="{ 'primary-btn--danger': confirmIsDestructive }" :disabled="confirmBusy" @click="confirmMove">
            <span v-if="confirmBusy" class="spinner" aria-hidden="true" />
            {{ confirmIsDestructive ? 'Uninstall' : 'Confirm & move' }}
          </button>
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

/* ---- Sidebar nav ---------------------------------------------------------- */
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

.pane {
  min-width: 0;
}

/* ---- Card shell ----------------------------------------------------------- */
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 20px;
  margin-bottom: 16px;
}

.card:last-child {
  margin-bottom: 0;
}

.card__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  margin: 0;
}

.card__head-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.card__sub {
  font-size: 12.5px;
  color: var(--faint);
  margin: 2px 0 8px;
}

.card__sub--tight {
  margin-bottom: 0;
}

/* ---- Setting row (label + control) ---------------------------------------- */
.srow {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 13px 0;
  border-top: 1px solid var(--border);
}

.srow--bordered {
  margin-top: 16px;
  padding-top: 14px;
}

.srow--advanced {
  border-top: none;
  padding: 13px 0 2px;
}

.srow__name {
  font-size: 13.5px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.srow__hint {
  font-size: 11.5px;
  color: var(--faint);
}

/* ---- Inputs --------------------------------------------------------------- */
.input {
  padding: 9px 12px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.input:disabled {
  opacity: 0.6;
  cursor: default;
}

.dur {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.input--num {
  width: 72px;
  padding: 9px 11px;
}

.input--wide {
  width: 80px;
  flex: none;
}

.input--unit {
  padding: 9px 10px;
  cursor: pointer;
}

.input--select {
  width: 100%;
  font-weight: var(--weight-semibold);
}

/* ---- Advanced disclosure -------------------------------------------------- */
.advanced {
  border-top: 1px solid var(--border);
  padding-top: 11px;
  margin-top: 2px;
}

.advanced__toggle {
  display: flex;
  align-items: center;
  gap: 7px;
  background: none;
  border: none;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  padding: 0;
}

.advanced__chev {
  transition: transform 0.15s;
}

.advanced__chev--open {
  transform: rotate(90deg);
}

/* ---- Read-only locked row ------------------------------------------------- */
.lrow {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--border);
}

.lrow__label {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 13.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.lrow__label-plain {
  font-size: var(--text-base);
  color: var(--muted);
}

.lrow__lock {
  display: inline-flex;
  color: var(--muted);
}

.lrow__val {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--text);
}

.lrow__val--muted {
  color: var(--muted);
}

/* ---- Card footer + save button -------------------------------------------- */
.card__foot,
.suwa-foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 14px;
}

.suwa-foot {
  margin-top: 16px;
}

.save-result {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
}

.save-result--ok {
  color: var(--set-ok-text);
}

.save-result--err {
  color: var(--danger-text);
}

.primary-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 20px;
  border-radius: var(--radius-lg);
  border: none;
  background: linear-gradient(135deg, var(--accent), var(--accentDeep));
  color: var(--cover-text);
  font-family: var(--font-sans);
  font-size: 13.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: opacity 0.15s;
}

.primary-btn:disabled {
  background: var(--surface3);
  color: var(--faint);
  cursor: default;
}

/* Destructive confirm (extension uninstall) — red primary. */
.primary-btn--danger {
  background: var(--danger);
  color: var(--on-danger);
}

.primary-btn--danger:disabled {
  background: var(--surface3);
  color: var(--faint);
}

/* Any in-flight row dims + blocks pointer input while its mutation runs (§16). */
.cat-row--busy,
.ext-card--busy,
.repo-row--busy {
  opacity: 0.6;
  pointer-events: none;
}

/* The small "Working…" marker shown beside a busy category row. */
.row-busy {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--muted);
}

/* ---- Categories ----------------------------------------------------------- */
.cat-row {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  margin-bottom: 9px;
  background: var(--surface2);
}

.cat-main {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.cat-edit {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 8px;
}

.cat-edit__input {
  flex: 1;
  border-color: var(--accent);
}

.cat-count {
  font-size: 11.5px;
  color: var(--faint);
}

.cat-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 6px;
}

/* Neutral, brand-tinted chip — categories are free-form user strings, so there's
   no per-category hue; one accent treatment reads cleanly in both themes. */
.chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.pill {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
}

/* ---- Reorder arrows ------------------------------------------------------- */
.reorder {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: none;
}

.reorder__btn {
  width: 24px;
  height: 18px;
  border-radius: var(--radius-xs);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  padding: 0;
}

.reorder__btn:disabled {
  color: var(--faint);
  opacity: 0.4;
  cursor: default;
}

/* ---- Buttons (mini / text / icon / solid / ghost) ------------------------- */
.mini-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.mini-btn:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}

.mini-btn--accent {
  border: none;
  background: var(--accent);
  color: var(--cover-text);
}

.mini-btn--accent:hover {
  color: var(--cover-text);
}

.text-btn {
  padding: 6px 9px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: transparent;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.text-btn:hover {
  color: var(--accentBright);
  border-color: var(--accent);
}

.icon-btn {
  width: 30px;
  height: 30px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s;
}

.icon-btn:hover {
  color: var(--text);
}

.icon-btn--danger {
  border-color: var(--border);
  color: var(--danger-bright);
}

.icon-btn--danger:hover {
  background: var(--danger-bg);
  color: var(--danger-bright);
}

.solid-btn {
  padding: 7px 13px;
  border-radius: var(--radius-md);
  border: none;
  background: var(--accent);
  color: var(--cover-text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.ghost-btn {
  padding: 10px 16px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: transparent;
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.ghost-btn--danger {
  padding: 7px 12px;
  border-color: var(--border);
  color: var(--danger-bright);
  font-size: var(--text-sm);
}

.ghost-btn--danger:hover {
  background: var(--danger-bg);
}

/* ---- Add row + inline form error ------------------------------------------ */
.add-row {
  display: flex;
  gap: 9px;
  margin-top: 13px;
}

.add-row__input {
  flex: 1;
}

.add-row__input--mono {
  font-family: var(--font-mono);
}

.form-error {
  margin: 6px 0 0;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--danger-text);
}

/* Pane-level error banner (extension actions) — sits above the tab content. */
.form-error--pane {
  margin: 0 0 12px;
  padding: 9px 13px;
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
}

/* ---- Engine --------------------------------------------------------------- */
.mode-badge {
  padding: 4px 11px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--set-ok-dot);
}

.status-text {
  font-size: var(--text-sm);
  color: var(--set-ok-text);
  font-weight: var(--weight-bold);
}

.mono {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text);
}

.engine-upgrade {
  border-top: 1px solid var(--border);
  padding-top: 14px;
  margin-top: 4px;
}

.uptodate {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-base);
  color: var(--set-ok-text);
  font-weight: var(--weight-bold);
}

.upgrade-avail {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.upgrade-avail__text {
  font-size: var(--text-base);
  color: var(--muted);
}

.upgrade-avail__text b {
  color: var(--text);
}

.stepper {
  margin-top: 14px;
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 14px 16px;
}

.step {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 0;
}

.step__dot {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--surface3);
  color: var(--faint);
}

.step__dot--done {
  background: var(--set-ok-bg);
  color: var(--set-ok-dot);
}

.step__dot--active {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.step__dot--failed {
  background: var(--danger-bg);
  color: var(--danger-bright);
}

.step__num {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.step__label {
  font-size: 13.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

/* ---- Suwayomi config fields ----------------------------------------------- */
.field-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-top: 16px;
}

.flare-body {
  margin-top: 16px;
}

.field {
  display: flex;
  flex-direction: column;
}

.field--full {
  grid-column: span 2;
}

.field--block {
  margin-bottom: 12px;
}

.field--inline {
  flex-direction: row;
  align-items: center;
  gap: 10px;
  align-self: end;
  padding-bottom: 2px;
}

.field__label {
  display: block;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
  margin-bottom: 6px;
}

.field__inline-label {
  font-size: 12.5px;
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

/* ---- Toggle switch -------------------------------------------------------- */
.switch {
  width: 44px;
  height: 25px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  border: 1px solid var(--border);
  position: relative;
  cursor: pointer;
  padding: 0;
  flex: none;
  transition: background 0.2s;
}

.switch--on {
  background: var(--accent);
}

.switch__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: var(--cover-text);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: left 0.2s;
}

.switch--on .switch__knob {
  left: 21px;
}

/* ---- Extension tabs + cards ----------------------------------------------- */
.ext-tabs {
  display: flex;
  align-items: center;
  gap: 9px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}

.tab {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 14px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.tab:hover {
  color: var(--text);
}

.tab--active {
  border-color: transparent;
  background: var(--accentSoft);
  color: var(--accentBright);
}

.tab__count {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--faint);
}

.tab__count--active {
  background: var(--accent);
  color: var(--cover-text);
}

.ext-actions {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}

.ext-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(420px, 1fr));
  gap: 10px;
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

/* ---- Repositories --------------------------------------------------------- */
.repo-row {
  display: flex;
  align-items: center;
  gap: 11px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 13px;
  margin-bottom: 9px;
}

.repo-row__url {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ---- Spinner -------------------------------------------------------------- */
.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--cover-text);
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: settings-spin 0.8s linear infinite;
}

.spinner--dark {
  border-color: currentColor;
  border-right-color: transparent;
  width: 13px;
  height: 13px;
}

.spinner--step {
  width: 13px;
  height: 13px;
  border-color: currentColor;
  border-right-color: transparent;
}

/* ---- Confirm modal -------------------------------------------------------- */
.modal {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: var(--cover-scrim);
}

.modal__card {
  width: 100%;
  max-width: 440px;
  background: var(--surface);
  border: 1px solid var(--border2);
  border-radius: var(--radius-2xl);
  padding: 24px;
  box-shadow: var(--shadow);
}

.modal__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: 18px;
  color: var(--text);
  margin-bottom: 6px;
}

.modal__text {
  font-size: var(--text-base);
  color: var(--muted);
  margin-bottom: 18px;
  line-height: 1.5;
}

.modal__field {
  margin-bottom: 18px;
}

.modal__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
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

@keyframes settings-spin {
  to { transform: rotate(360deg); }
}

@keyframes settings-shimmer {
  to { transform: translateX(100%); }
}
</style>
