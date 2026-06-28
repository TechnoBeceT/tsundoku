<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import DurationInput from '../ui/DurationInput.vue'
import FormError from '../ui/FormError.vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import ExtensionRow from './ExtensionRow.vue'
import RepoRow from './RepoRow.vue'
import SettingRow from './SettingRow.vue'
import type { MoveDirection } from '../ui/controls.types'
import {
  ADD_ACTION_ID,
  type DurationValue,
  type Extension,
  type ExtensionTab,
  type Repo,
  type ReorderDirection,
  type RowActionState,
} from '../screens/settings.types'

/**
 * ExtensionsPane — the Sources & Extensions pane: a SegmentedTabs switch between
 * Installed (with a check-for-updates action), Available (installable), and
 * Repositories (reorderable repo list + add-row + the read-only update-check
 * cadence). A failed extension mutation surfaces in a pane-level banner; the
 * destructive uninstall routes through a confirm modal (§16/§2e).
 *
 *   - `extensions` / `availableExtensions`: installed + installable sets.
 *   - `repos`: the repository URL list.
 *   - `extensionAction` / `repoAction`: the §16 per-row mutation state.
 *   - `extCheckInterval`: the (read-only here) update-check cadence.
 *   - `checkingUpdates`: whether a check-for-updates call is in flight.
 */
const props = withDefaults(defineProps<{
  /** Installed extensions. */
  extensions: Extension[]
  /** Available (installable) extensions. */
  availableExtensions: Extension[]
  /** Extension repository URLs. */
  repos: Repo[]
  /** §16 state of extension mutations (busy pkgName + error). */
  extensionAction?: RowActionState
  /** §16 state of repo mutations (busy id + error). */
  repoAction?: RowActionState
  /** Background extension update-check cadence (read-only here). */
  extCheckInterval: DurationValue
  /** Whether a check-for-updates call is in flight. */
  checkingUpdates?: boolean
}>(), {
  extensionAction: () => ({ busyId: null }),
  repoAction: () => ({ busyId: null }),
  checkingUpdates: false,
})

const emit = defineEmits<{
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
  /** Move a repository up (−1) or down (+1). */
  'reorder-repo': [payload: { id: string, direction: ReorderDirection }]
  /** The extension update-check cadence was changed by the user. */
  'update:extCheckInterval': [DurationValue]
}>()

const extTab = ref<ExtensionTab>('installed')
const tabs = computed(() => [
  { key: 'installed', label: 'Installed', count: props.extensions.length },
  { key: 'available', label: 'Available', count: props.availableExtensions.length },
  { key: 'repos', label: 'Repositories', count: props.repos.length },
])

// Per-row busy flags (the parent flags the single in-flight pkgName / repo id).
const extensionRowBusy = (id: string): boolean => props.extensionAction.busyId === id
const repoRowBusy = (id: string): boolean => props.repoAction.busyId === id
const repoAddBusy = computed(() => props.repoAction.busyId === ADD_ACTION_ID)

// Repo rows + reorder-arrow enablement (top can't go up, bottom can't go down).
const repoRows = computed(() =>
  props.repos.map((r, i, arr) => ({
    ...r,
    canMoveUp: i > 0,
    canMoveDown: i < arr.length - 1,
  })),
)

const newRepo = ref('')
const repoError = ref('')
// One inline repo error = local URL validation OR the parent's backend failure.
const repoErrorMsg = computed(() => repoError.value || (props.repoAction.error ?? ''))
function addRepo() {
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

// ---- Uninstall confirm (destructive, §2e) ---------------------------------
const confirmExt = ref<{ id: string, name: string } | null>(null)
const confirmBusy = computed(() =>
  confirmExt.value ? props.extensionAction.busyId === confirmExt.value.id : false)

function startUninstall(e: Extension) {
  confirmExt.value = { id: e.id, name: e.name }
}
function confirmUninstall() {
  const c = confirmExt.value
  if (!c || confirmBusy.value) return
  emit('uninstall-extension', c.id)
  // No async wiring (Storybook) → close now; else the watcher closes on completion.
  if (!confirmBusy.value) confirmExt.value = null
}
function cancelUninstall() {
  confirmExt.value = null
}
watch(confirmBusy, (busy, prev) => {
  if (prev && !busy) confirmExt.value = null
})

function onRepoMove(id: string, direction: MoveDirection) {
  emit('reorder-repo', { id, direction })
}
</script>

<template>
  <div class="ext-tabs">
    <SegmentedTabs v-model="extTab" :tabs="tabs" />
  </div>

  <!-- A failed extension mutation is surfaced inline for the whole pane. -->
  <p v-if="extensionAction.error" class="form-error--pane">{{ extensionAction.error }}</p>

  <!-- Installed -->
  <template v-if="extTab === 'installed'">
    <div class="ext-actions">
      <AppButton variant="mini" size="sm" :loading="checkingUpdates" @click="emit('check-updates')">Check for updates</AppButton>
    </div>
    <div class="ext-grid">
      <ExtensionRow
        v-for="e in extensions"
        :key="e.id"
        :extension="e"
        installed
        :busy="extensionRowBusy(e.id)"
        @update="emit('update-extension', e.id)"
        @uninstall="startUninstall(e)"
      />
    </div>
  </template>

  <!-- Available -->
  <template v-else-if="extTab === 'available'">
    <div class="ext-grid">
      <ExtensionRow
        v-for="e in availableExtensions"
        :key="e.id"
        :extension="e"
        :busy="extensionRowBusy(e.id)"
        @install="emit('install-extension', e.id)"
      />
    </div>
  </template>

  <!-- Repositories -->
  <template v-else>
    <RepoRow
      v-for="r in repoRows"
      :key="r.id"
      :repo="r"
      :can-up="r.canMoveUp"
      :can-down="r.canMoveDown"
      :busy="repoRowBusy(r.id)"
      @move="onRepoMove(r.id, $event)"
      @remove="emit('remove-repo', r.id)"
    />

    <div v-if="repoErrorMsg" class="repo-error">
      <FormError :message="repoErrorMsg" />
    </div>
    <div class="add-row">
      <input v-model="newRepo" class="add-row__input add-row__input--mono" placeholder="https://…/index.min.json" :disabled="repoAddBusy" @keydown.enter="addRepo">
      <AppButton variant="primary" size="md" :loading="repoAddBusy" @click="addRepo">Add repo</AppButton>
    </div>

    <SettingRow spaced name="Extension update check" hint="How often to auto-check for extension updates">
      <DurationInput :model-value="extCheckInterval" @update:model-value="emit('update:extCheckInterval', $event)" />
    </SettingRow>
  </template>

  <!-- Destructive uninstall confirm (brief §2e). -->
  <ConfirmModal
    :open="!!confirmExt"
    :title="confirmExt ? `Uninstall “${confirmExt.name}”?` : ''"
    message="This removes the source extension from the engine. Downloaded chapters stay on disk."
    confirm-label="Uninstall"
    destructive
    :busy="confirmBusy"
    @confirm="confirmUninstall"
    @update:open="(v) => { if (!v) cancelUninstall() }"
  />
</template>

<style scoped>
.ext-tabs {
  margin-bottom: 16px;
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

/* ---- Add row + inline form error ------------------------------------------ */
.add-row {
  display: flex;
  gap: 9px;
  margin-top: 13px;
}

.add-row__input {
  flex: 1;
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

.add-row__input--mono {
  font-family: var(--font-mono);
}

.add-row__input::placeholder {
  color: var(--faint);
}

.add-row__input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.add-row__input:disabled {
  opacity: 0.6;
  cursor: default;
}

/* Inline repo validation/backend error — the shared FormError atom, nudged below
   the repo list (the old bespoke line carried this 6px top margin itself). */
.repo-error {
  margin-top: 6px;
}

/* Pane-level error banner (a failed extension mutation) — a boxed danger panel
   above the tab content; distinct from the inline FormError line, so kept
   bespoke here (a future ErrorBanner-style atom could absorb it). */
.form-error--pane {
  margin: 0 0 12px;
  padding: 9px 13px;
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--danger-text);
}
</style>
