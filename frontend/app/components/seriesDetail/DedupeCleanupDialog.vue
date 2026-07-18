<script setup lang="ts">
import { computed } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import type { DedupePlanItem, DedupeReason } from '../screens/seriesDetail.types'

/**
 * DedupeCleanupDialog — the preview→confirm step for "Remove duplicate files".
 *
 * The owner used to press the button and the sweep ran immediately with no idea
 * what it would touch. Now the button first fetches the dry-run plan
 * (`GET /api/series/:id/dedupe-files`) and this dialog lists EXACTLY what the
 * destructive POST will delete, grouped by reason, so the removal is confirmed —
 * not a leap of faith. The list is provably identical to what the POST removes
 * (both derive from the same backend plan).
 *
 * An EMPTY plan renders "Nothing to remove" and offers only Close — the POST never
 * fires when there is nothing to delete.
 *
 * Presentation-only: the plan arrives via `items`, `confirm` emits nothing (the
 * POST takes no body — it deletes the whole plan the backend re-computes), and the
 * parent runs the POST. §16: `busy` spins the confirm button and blocks dismissal;
 * a FAILED removal keeps the dialog open with the reason shown inside it (the
 * parent closes it only once the removal succeeded).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The removal plan items (the server-computed dry-run). */
  items: DedupePlanItem[]
  /** The removal POST is in flight — spins confirm + blocks dismissal. */
  busy?: boolean
  /** A failed-removal message shown INSIDE the dialog, or null for none. */
  error?: string | null
}>(), {
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Removal confirmed — the parent runs the (body-less) POST. */
  'confirm': []
}>()

/**
 * REASON_ORDER — the fixed display order + copy for each removal source, so the
 * list always groups the same way (row-deleting passes first, file-only last).
 */
const REASON_ORDER: { key: DedupeReason, label: string, hint: string }[] = [
  {
    key: 'epilogue-merge',
    label: 'Duplicate chapter rows',
    hint: 'Engine-switch twins — a chapter the old engine numbered "-1" and the new one re-keyed. The duplicate row and its file are removed; the canonical is kept.',
  },
  {
    key: 'ignored-fractional',
    label: 'Ignored fractional chapters',
    hint: 'Fractional chapters downloaded before every source carrying them was set to “Ignore fractional chapters”. The chapter row and its file are removed.',
  },
  {
    key: 'orphan-superseded',
    label: 'Orphan / duplicate files',
    hint: 'Leftover CBZ files no chapter owns — a superseded split part, or a duplicate of a chapter’s winning file. Removed from disk only.',
  },
]

interface DedupeGroup {
  key: DedupeReason
  label: string
  hint: string
  items: DedupePlanItem[]
}

/** The plan grouped into its non-empty reason sections, in REASON_ORDER. */
const groups = computed<DedupeGroup[]>(() =>
  REASON_ORDER
    .map((r) => ({ ...r, items: props.items.filter((it) => it.reason === r.key) }))
    .filter((g) => g.items.length > 0),
)

const total = computed(() => props.items.length)

function confirm(): void {
  if (total.value === 0 || props.busy) return
  emit('confirm')
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="busy"
    title="Remove duplicate files"
    @update:open="emit('update:open', $event)"
  >
    <ErrorBanner v-if="error" class="dedupe__error" :message="error" :dismissible="false" />

    <div v-if="total === 0" class="dedupe__empty">
      Nothing to remove — this series has no duplicate or orphan files.
    </div>

    <template v-else>
      <div class="dedupe__head">
        <span class="dedupe__count">{{ total }} item{{ total === 1 ? '' : 's' }} to remove</span>
      </div>

      <p class="dedupe__note">
        Removes the files (and the duplicate/ignored chapter rows) listed below. Winning files and
        canonical chapters are always kept. <strong>This cannot be undone.</strong>
      </p>

      <section v-for="group in groups" :key="group.key" class="dedupe-group">
        <header class="dedupe-group__head">
          <span class="dedupe-group__label">{{ group.label }}</span>
          <span class="dedupe-group__count">{{ group.items.length }}</span>
        </header>
        <p class="dedupe-group__hint">{{ group.hint }}</p>
        <ul class="dedupe-group__list">
          <li v-for="item in group.items" :key="item.filename" class="dedupe-row">
            <span class="dedupe-row__number">{{ item.number === null ? '—' : item.number }}</span>
            <span class="dedupe-row__file">{{ item.filename }}</span>
          </li>
        </ul>
      </section>
    </template>

    <template #actions>
      <AppButton variant="ghost" :disabled="busy" @click="emit('update:open', false)">
        {{ total === 0 ? 'Close' : 'Cancel' }}
      </AppButton>
      <AppButton
        v-if="total > 0"
        variant="danger-ghost"
        :loading="busy"
        @click="confirm"
      >
        Remove {{ total }} item{{ total === 1 ? '' : 's' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.dedupe__error {
  margin-bottom: 14px;
}

.dedupe__empty {
  padding: 8px 0 4px;
  font-size: 13px;
  color: var(--muted);
}

.dedupe__head {
  margin-bottom: 8px;
}

.dedupe__count {
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.dedupe__note {
  margin: 0 0 14px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--muted);
}

.dedupe__note strong {
  color: var(--text);
  font-weight: var(--weight-bold);
}

.dedupe-group {
  margin-bottom: 16px;
}

.dedupe-group:last-child {
  margin-bottom: 0;
}

.dedupe-group__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 4px;
}

.dedupe-group__label {
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.dedupe-group__count {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  color: var(--faint);
}

.dedupe-group__hint {
  margin: 0 0 8px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--muted);
}

.dedupe-group__list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.dedupe-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.dedupe-row__number {
  min-width: 52px;
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.dedupe-row__file {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--muted);
}
</style>
