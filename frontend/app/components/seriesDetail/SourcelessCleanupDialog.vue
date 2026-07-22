<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import Checkbox from '../ui/Checkbox.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import type { SourcelessCleanupChapter, SourcelessCleanupPreview } from '../screens/sourceless.types'

/**
 * SourcelessCleanupDialog — the owner-triggered removal of already-downloaded
 * chapters left behind once every source that ever supplied them has been
 * removed (Rule 2: never-auto-delete — the `Chapter` row survives source
 * removal on purpose, GAP-101/QCAT-303; this dialog is the one OWNER-initiated
 * path that clears the resulting orphan).
 *
 * Unlike `FractionalCleanupDialog` there is no page-count "yardstick" here:
 * a fractional chapter might secretly be full-size content wearing a `.5`
 * number, but a SOURCELESS chapter carries no such ambiguity — every row in
 * the preview is, by construction, a downloaded chapter no remaining source
 * can satisfy. So every row starts pre-ticked and the owner opts OUT (via a
 * per-row box or the "select all" toggle) rather than opting in.
 *
 * 🔴 QCAT-222 (owner-ratified, NON-NEGOTIABLE): this delete has NO in-product
 * inverse — it permanently deletes CBZ files, and no source can restore them
 * (that carrier is already gone). It MUST NOT fire off the list's own
 * "Delete N files" button directly: that button only opens the shared,
 * destructive `ConfirmModal` (mirrors `RemoveSourceDialog` /
 * `PurgeSourceDialog`'s use of the same atom), which alone emits `confirm`.
 *
 * Presentation-only: the preview arrives via props, `confirm` carries ONLY the
 * ticked chapter ids, and the parent runs the POST. §16: `busy` spins the
 * confirm button and blocks dismissal; a FAILED removal keeps the dialog open
 * with the reason shown inside it (the parent closes it only once the
 * removal succeeded).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown. */
  open: boolean
  /** The series title (currently display-only via the Dialog title; kept for parity/future use). */
  seriesTitle: string
  /** The removable sourceless chapters (the server-computed preview), or null while loading. */
  preview: SourcelessCleanupPreview | null
  /** The removal POST is in flight — spins confirm + blocks dismissal. */
  busy?: boolean
  /** A failed-removal message shown INSIDE the dialog, or null for none. */
  error?: string | null
}>(), {
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The dialog was dismissed (Cancel, Escape, overlay click, or the close button). */
  'close': []
  /** Removal confirmed — carries ONLY the ticked chapter ids (never the whole preview). */
  'confirm': [chapterIds: string[]]
}>()

const chapters = computed<SourcelessCleanupChapter[]>(() => props.preview?.chapters ?? [])

/** The ticked chapter ids. Seeded (all-ticked) every time the dialog opens. */
const selected = ref<Set<string>>(new Set())

function seedSelection(): void {
  selected.value = new Set(chapters.value.map((c) => c.chapterId))
}

// Re-seed on every open (mirrors the other Series-Detail dialogs' reset-on-open)
// so a re-open never inherits a stale tick state, and on a preview change so a
// refreshed set is never rendered against last set's ticks.
watch(() => [props.open, props.preview] as const, ([isOpen]) => {
  if (isOpen) seedSelection()
}, { immediate: true })

/** Whether the destructive QCAT-222 confirm step is showing. */
const confirming = ref(false)

// Reset the confirm step on every (re)open — a re-open never resumes mid-confirm.
watch(() => props.open, (isOpen) => {
  if (isOpen) confirming.value = false
})

interface CleanupRow extends SourcelessCleanupChapter {
  /** Ticked = this file will be deleted on confirm. */
  checked: boolean
}

const rows = computed<CleanupRow[]>(() =>
  chapters.value.map((c) => ({ ...c, checked: selected.value.has(c.chapterId) })),
)

const selectedIds = computed<string[]>(() =>
  chapters.value.map((c) => c.chapterId).filter((id) => selected.value.has(id)),
)

const selectedCount = computed(() => selectedIds.value.length)

const allSelected = computed(() => chapters.value.length > 0 && selectedCount.value === chapters.value.length)

function setChecked(chapterId: string, checked: boolean): void {
  const next = new Set(selected.value)
  if (checked) next.add(chapterId)
  else next.delete(chapterId)
  selected.value = next
}

function toggleAll(checked: boolean): void {
  selected.value = checked ? new Set(chapters.value.map((c) => c.chapterId)) : new Set()
}

/** Opens the QCAT-222 confirm gate — never deletes anything by itself. */
function requestConfirm(): void {
  if (selectedCount.value === 0 || props.busy) return
  confirming.value = true
}

/** Only reachable from the ConfirmModal's own confirm button. */
function onConfirmed(): void {
  emit('confirm', [...selectedIds.value])
}

const confirmTitle = computed(() => `Delete ${selectedCount.value} file${selectedCount.value === 1 ? '' : 's'}?`)
</script>

<template>
  <Dialog
    :open="open"
    :busy="busy"
    title="Remove sourceless chapters"
    @close="emit('close')"
  >
    <ErrorBanner v-if="error" class="src__error" :message="error" :dismissible="false" />

    <div class="src__head">
      <span class="src__count">
        {{ chapters.length }} removable chapter{{ chapters.length === 1 ? '' : 's' }}
      </span>
    </div>

    <p class="src__note">
      These downloaded chapters are carried by no source. Removing them deletes the CBZ files
      permanently — no source can restore them.
    </p>

    <label v-if="chapters.length > 0" class="src__selectall">
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). Same footgun as Dialog.vue. -->
      <Checkbox :model-value="allSelected" :disabled="busy" :ariaLabel="'Select all chapters'" @update:model-value="toggleAll" />
      <span>Select all</span>
    </label>

    <ul class="src__list">
      <li v-for="row in rows" :key="row.chapterId" class="src-row">
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). Same footgun as Dialog.vue. -->
        <Checkbox :model-value="row.checked" :disabled="busy" :ariaLabel="`Remove chapter ${row.number ?? row.filename}`" @update:model-value="setChecked(row.chapterId, $event)" />
        <span class="src-row__number">{{ row.number ?? '—' }}</span>
        <span class="src-row__pages">{{ row.pageCount === null ? '—' : `${row.pageCount}p` }}</span>
        <span class="src-row__filename" :title="row.filename">{{ row.filename }}</span>
        <span class="src-row__provider">{{ row.provider || '—' }}</span>
      </li>
    </ul>

    <template #actions>
      <AppButton variant="ghost" :disabled="busy" @click="emit('close')">
        Cancel
      </AppButton>
      <AppButton
        variant="danger-ghost"
        :disabled="selectedCount === 0 || busy"
        @click="requestConfirm"
      >
        Delete {{ selectedCount }} file{{ selectedCount === 1 ? '' : 's' }}
      </AppButton>
    </template>

    <!-- QCAT-222: no in-product inverse for this delete, so the actual removal
         can ONLY be triggered from this shared, destructive confirm gate — never
         directly from the button above. -->
    <ConfirmModal
      :open="confirming"
      :busy="busy"
      :title="confirmTitle"
      message="No source can restore these chapters. The CBZ files will be permanently deleted."
      confirm-label="Delete files"
      destructive
      @update:open="confirming = $event"
      @confirm="onConfirmed"
    />
  </Dialog>
</template>

<style scoped>
.src__error {
  margin-bottom: 14px;
}

.src__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.src__count {
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.src__note {
  margin: 0 0 14px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--muted);
}

.src__selectall {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  color: var(--muted);
  cursor: pointer;
}

.src__list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.src-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 12px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.src-row__number {
  min-width: 44px;
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.src-row__pages {
  min-width: 48px;
  font-size: 13px;
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.src-row__filename {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--text);
}

.src-row__provider {
  flex: none;
  max-width: 96px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--muted);
}
</style>
