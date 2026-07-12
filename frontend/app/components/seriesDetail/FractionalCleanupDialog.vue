<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import Checkbox from '../ui/Checkbox.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import type { FractionalCleanupChapter } from '../screens/seriesDetail.types'

/**
 * FractionalCleanupDialog — the owner-triggered removal of already-downloaded
 * FRACTIONAL chapters (the files the "Ignore fractional chapters" switch leaves
 * behind: that switch stops NEW fractional downloads and deletes nothing).
 *
 * 🔴 WHY THIS IS A DIALOG AND NOT A BUTTON. On the owner's real library the
 * removable set held two 1-page notice pages (181.5, 190.5) AND two chapters
 * numbered 221.5 / 223.5 that are 132 and 135 pages — FULL-SIZE chapters against
 * a ~96p typical. A blunt "delete every fractional" would have destroyed 267
 * pages of real content. THE PAGE COUNT IS THE EVIDENCE; the number and the
 * source are only labels. So the dialog SHOWS the evidence (with
 * `typicalPageCount` as the yardstick) and the owner chooses.
 *
 * PRE-TICK RULE — ADVISORY ONLY. It decides what is PRE-TICKED, never what is
 * deleted (the owner always confirms, and the backend re-computes the removable
 * set anyway). A chapter is pre-UNTICKED and flagged "⚠ full-size chapter" when
 * `pageCount >= 0.5 × typicalPageCount`. Two guards, both deliberate:
 *   - `typicalPageCount <= 0` (the series has no whole downloaded chapter to
 *     measure against) → there is no yardstick, so NOTHING is pre-ticked and
 *     NOTHING is flagged: with no evidence the machine must not pre-decide.
 *   - `pageCount === null` (never recorded) → same reasoning: not pre-ticked, and
 *     NOT flagged full-size (that would be a claim we cannot support).
 *
 * Presentation-only: the preview arrives via props, the confirm emits the TICKED
 * chapter ids, and the parent runs the POST. §16: `busy` spins the confirm button
 * and blocks dismissal; a FAILED removal keeps the dialog open with the reason
 * shown inside it (the parent closes it only once the removal succeeded).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The removable fractional chapters (the server-computed preview). */
  chapters: FractionalCleanupChapter[]
  /** MEDIAN page count of the series' whole downloaded chapters — the yardstick; 0 = unknown. */
  typicalPageCount?: number
  /** The removal POST is in flight — spins confirm + blocks dismissal. */
  busy?: boolean
  /** A failed-removal message shown INSIDE the dialog, or null for none. */
  error?: string | null
}>(), {
  typicalPageCount: 0,
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Removal confirmed — carries ONLY the ticked chapter ids (never the whole preview). */
  'confirm': [chapterIds: string[]]
}>()

/**
 * isFullSize — the advisory heuristic: a fractional whose file is at least half a
 * typical chapter is a real chapter wearing a ".5" number, not a notice page.
 * False when there is no yardstick (typical <= 0) or no measurement (null pages).
 */
function isFullSize(pageCount: number | null, typical: number): boolean {
  if (typical <= 0 || pageCount === null) return false
  return pageCount >= 0.5 * typical
}

/** Judgeable = we have both a yardstick and a measurement, so a pre-tick is honest. */
function isJudgeable(pageCount: number | null, typical: number): boolean {
  return typical > 0 && pageCount !== null
}

/** The ticked chapter ids. Seeded by the pre-tick rule every time the dialog opens. */
const selected = ref<Set<string>>(new Set())

function seedSelection(): void {
  selected.value = new Set(
    props.chapters
      .filter((c) => isJudgeable(c.pageCount, props.typicalPageCount) && !isFullSize(c.pageCount, props.typicalPageCount))
      .map((c) => c.chapterId),
  )
}

// Re-seed on every open (mirrors the other Series-Detail dialogs' reset-on-open)
// so a re-open never inherits a stale tick state, and on a preview change so a
// refreshed set is never rendered against last set's ticks.
watch(() => [props.open, props.chapters] as const, ([isOpen]) => {
  if (isOpen) seedSelection()
}, { immediate: true })

interface CleanupRow extends FractionalCleanupChapter {
  /** Ticked = this file will be deleted on confirm. */
  checked: boolean
  /** Flagged "⚠ full-size chapter" — real content, judged by pages not by number. */
  fullSize: boolean
}

const rows = computed<CleanupRow[]>(() =>
  props.chapters.map((c) => ({
    ...c,
    checked: selected.value.has(c.chapterId),
    fullSize: isFullSize(c.pageCount, props.typicalPageCount),
  })),
)

const selectedIds = computed<string[]>(() =>
  props.chapters.map((c) => c.chapterId).filter((id) => selected.value.has(id)),
)

const selectedCount = computed(() => selectedIds.value.length)

function setChecked(chapterId: string, checked: boolean): void {
  const next = new Set(selected.value)
  if (checked) next.add(chapterId)
  else next.delete(chapterId)
  selected.value = next
}

function confirm(): void {
  if (selectedCount.value === 0 || props.busy) return
  emit('confirm', [...selectedIds.value])
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="busy"
    title="Remove fractional files"
    @update:open="emit('update:open', $event)"
  >
    <ErrorBanner v-if="error" class="frac__error" :message="error" :dismissible="false" />

    <div class="frac__head">
      <span class="frac__count">
        {{ chapters.length }} removable chapter{{ chapters.length === 1 ? '' : 's' }}
      </span>
      <span v-if="typicalPageCount > 0" class="frac__typical">typical chapter: {{ typicalPageCount }}p</span>
    </div>

    <p class="frac__note">
      Deletes each ticked chapter's CBZ file and its chapter row. The source's feed is kept —
      un-ticking “Ignore fractional chapters” restores the chapter on the next refresh.
      <strong>Page count is the evidence: judge by it, not by the number.</strong>
    </p>

    <ul class="frac__list">
      <li v-for="row in rows" :key="row.chapterId" class="frac-row" :class="{ 'frac-row--warn': row.fullSize }">
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). Same footgun as Dialog.vue. -->
        <Checkbox :model-value="row.checked" :disabled="busy" :ariaLabel="`Remove chapter ${row.number}`" @update:model-value="setChecked(row.chapterId, $event)" />
        <span class="frac-row__number">{{ row.number }}</span>
        <span class="frac-row__pages">{{ row.pageCount === null ? '—' : `${row.pageCount}p` }}</span>
        <span class="frac-row__provider">{{ row.provider || '—' }}</span>
        <span v-if="row.fullSize" class="frac-row__warn">⚠ full-size chapter</span>
      </li>
    </ul>

    <template #actions>
      <AppButton variant="ghost" :disabled="busy" @click="emit('update:open', false)">
        Cancel
      </AppButton>
      <AppButton
        variant="danger-ghost"
        :loading="busy"
        :disabled="selectedCount === 0"
        @click="confirm"
      >
        Remove {{ selectedCount }} file{{ selectedCount === 1 ? '' : 's' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.frac__error {
  margin-bottom: 14px;
}

.frac__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.frac__count {
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.frac__typical {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--faint);
}

.frac__note {
  margin: 0 0 14px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--muted);
}

.frac__note strong {
  color: var(--text);
  font-weight: var(--weight-bold);
}

.frac__list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.frac-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 12px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.frac-row--warn {
  border-color: var(--danger-border);
  background: var(--danger-bg);
}

.frac-row__number {
  min-width: 52px;
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.frac-row__pages {
  min-width: 48px;
  font-size: 13px;
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.frac-row__provider {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--muted);
}

.frac-row__warn {
  flex: none;
  font-size: 11.5px;
  font-weight: var(--weight-bold);
  color: var(--danger-text);
}
</style>
