<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import FormError from '../ui/FormError.vue'
import SelectField from '../ui/SelectField.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import CategoryRow from './CategoryRow.vue'
import type { MoveDirection } from '../ui/controls.types'
import {
  ADD_ACTION_ID,
  type ReorderDirection,
  type RowActionState,
  type SettingsCategory,
} from '../screens/settings.types'

/**
 * CategoriesPane — the user-definable category CRUD list: a reorderable row per
 * category (add / inline-rename / set-default / delete) plus the add-row and the
 * confirm modal for the folder-moving rename/delete actions.
 *
 * Client-side validation (name shape + uniqueness) is surfaced inline; the
 * parent's backend failure (`categoryAction.error`) shares the same line, and the
 * in-flight row dims via `categoryAction.busyId` (§16). All mutations are emitted.
 *
 *   - `categories`: the category list.
 *   - `categoryAction`: the §16 per-row mutation state (busy row + error).
 */
const props = withDefaults(defineProps<{
  /** The category list. */
  categories: SettingsCategory[]
  /** §16 state of category mutations (busy row + inline error). */
  categoryAction?: RowActionState
}>(), {
  categoryAction: () => ({ busyId: null }),
})

const emit = defineEmits<{
  /** Add a new category by name. */
  'add-category': [name: string]
  /** Rename a category. */
  'rename-category': [payload: { id: string, name: string }]
  /** Move a category up (−1) or down (+1). */
  'reorder-category': [payload: { id: string, direction: ReorderDirection }]
  /** Delete a category; `targetId` is the reassign target ("" when empty). */
  'delete-category': [payload: { id: string, targetId: string }]
  /** Mark a category as the default landing for new series. */
  'set-default-category': [id: string]
}>()

// Each row plus its reorder-arrow enablement (top can't go up, bottom can't go down).
const rows = computed(() =>
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

/** The folder-moving confirm: a category rename or delete (both move series folders). */
type ConfirmState =
  | { kind: 'rename', id: string, name: string, from: string, count: number }
  | { kind: 'delete', id: string, name: string, count: number, targetId: string }
const confirmModal = ref<ConfirmState | null>(null)

// The inline error line = local client validation OR the parent's backend failure.
const categoryErrorMsg = computed(() => categoryError.value || (props.categoryAction.error ?? ''))
// Per-row busy: the single category whose mutation the parent flagged in flight.
const rowBusy = (id: string): boolean => props.categoryAction.busyId === id
const addBusy = computed(() => props.categoryAction.busyId === ADD_ACTION_ID)

const nameTaken = (name: string, exceptId?: string): boolean =>
  props.categories.some(c => c.id !== exceptId && c.name.toLowerCase() === name.toLowerCase())

function addCategory() {
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

function startRename(c: SettingsCategory) {
  renameId.value = c.id
  renameVal.value = c.name
  categoryError.value = ''
}
function cancelRename() {
  renameId.value = null
}
function saveRename(c: SettingsCategory) {
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
  // Renaming a non-empty category physically moves its series folders — confirm.
  if (c.count > 0) {
    confirmModal.value = { kind: 'rename', id: c.id, name: to, from: c.name, count: c.count }
  }
  else {
    emit('rename-category', { id: c.id, name: to })
  }
}

function startDelete(c: SettingsCategory) {
  const target = props.categories.find(x => x.id !== c.id)
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

// The reassign-target options for the open delete modal (every other category).
const targetOptions = computed(() => {
  const c = confirmModal.value
  if (!c) return []
  return props.categories.filter(x => x.id !== c.id).map(x => ({ value: x.id, label: x.name }))
})
// The confirm narrowed to its delete variant (or null) — lets the template bind
// the reassign-target select without a union-narrowing dance.
const deleteModal = computed(() => {
  const c = confirmModal.value
  return c?.kind === 'delete' ? c : null
})
function setTarget(value: string) {
  if (confirmModal.value?.kind === 'delete') confirmModal.value.targetId = value
}

// Whether the open confirm's action is in flight — drives the spinner + the
// close-on-completion watcher.
const confirmBusy = computed(() =>
  confirmModal.value ? props.categoryAction.busyId === confirmModal.value.id : false)

function confirmMove() {
  const c = confirmModal.value
  if (!c || confirmBusy.value) return
  if (c.kind === 'rename') emit('rename-category', { id: c.id, name: c.name })
  else emit('delete-category', { id: c.id, targetId: c.targetId })
  // With no async wiring (Storybook) close immediately; otherwise the confirmBusy
  // false-edge watcher closes it once the action completes.
  if (!confirmBusy.value) confirmModal.value = null
}
function cancelConfirm() {
  confirmModal.value = null
}
watch(confirmBusy, (busy, prev) => {
  if (prev && !busy) confirmModal.value = null
})

const confirmTitle = computed(() => {
  const c = confirmModal.value
  if (!c) return ''
  return c.kind === 'rename' ? `Rename “${c.from}” → “${c.name}”?` : `Delete “${c.name}”?`
})
const confirmMessage = computed(() => {
  const c = confirmModal.value
  if (!c) return ''
  const plural = c.count > 1 ? 's' : ''
  return c.kind === 'rename'
    ? `This physically moves ${c.count} series folder${plural} on disk and rewrites their sidecars.`
    : `Choose where its ${c.count} series go — their folders move on disk. CBZ files are never deleted.`
})

// Reorder direction is the same ±1 union the ReorderControl emits.
function onMove(id: string, direction: MoveDirection) {
  emit('reorder-category', { id, direction })
}
</script>

<template>
  <SurfaceCard
    title="Categories"
    sub="User-defined. Renaming or deleting moves series folders on disk — CBZ files are never deleted."
  >
    <CategoryRow
      v-for="c in rows"
      :key="c.id"
      :category="c"
      :can-up="c.canMoveUp"
      :can-down="c.canMoveDown"
      :busy="rowBusy(c.id)"
      :renaming="renameId === c.id"
      :rename-value="renameVal"
      @update:rename-value="renameVal = $event"
      @move="onMove(c.id, $event)"
      @start-rename="startRename(c)"
      @save-rename="saveRename(c)"
      @cancel-rename="cancelRename"
      @set-default="emit('set-default-category', c.id)"
      @start-delete="startDelete(c)"
    />

    <div v-if="categoryErrorMsg" class="cat-error">
      <FormError :message="categoryErrorMsg" />
    </div>
    <div class="add-row">
      <input v-model="newCategory" class="add-row__input" placeholder="New category name…" :disabled="addBusy" @keydown.enter="addCategory">
      <AppButton variant="primary" size="md" :loading="addBusy" @click="addCategory">
        <template #icon>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
        </template>
        Add
      </AppButton>
    </div>
  </SurfaceCard>

  <!-- Folder-moving confirm: category rename or delete. -->
  <ConfirmModal
    :open="!!confirmModal"
    :title="confirmTitle"
    :message="confirmMessage"
    confirm-label="Confirm & move"
    :busy="confirmBusy"
    @confirm="confirmMove"
    @update:open="(v) => { if (!v) cancelConfirm() }"
  >
    <div v-if="deleteModal" class="modal__field">
      <span class="field__label">Move series to</span>
      <SelectField
        :model-value="deleteModal.targetId"
        :options="targetOptions"
        aria-label="Move series to"
        @update:model-value="setTarget"
      />
    </div>
  </ConfirmModal>
</template>

<style scoped>
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

/* Inline validation/backend error — the shared FormError atom, nudged below the
   category list (the old bespoke line carried this 6px top margin itself). */
.cat-error {
  margin-top: 6px;
}

/* ---- Confirm modal reassign-target field ---------------------------------- */
.modal__field {
  margin-bottom: 4px;
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

.modal__field :deep(.select) {
  width: 100%;
}
</style>
