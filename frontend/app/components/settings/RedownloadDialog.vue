<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Checkbox from '../ui/Checkbox.vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import TextField from '../ui/TextField.vue'
import type { RedownloadFilter, RedownloadPreview } from '~/composables/useRedownload'

/**
 * RedownloadDialog — the filter + preview + confirm gate for the LIBRARY-WIDE
 * bulk re-download (`GET`/`POST /api/downloads/redownload`). It re-queues
 * already-downloaded chapters from one source so the engine fetches them again:
 * the remedy when a source's stored bytes turn out to be wrong while every state
 * field still says the chapters are fine.
 *
 * Two steps, never one. "Check" loads the server-computed preview (how many
 * chapters, how many download cycles) and mutates nothing; only the confirm gate
 * applies it. The preview is cleared whenever the filter changes, so the count on
 * screen always belongs to the filter on screen.
 *
 * 🔴 QCAT-222: a sweep across the whole library MUST NOT fire off its own button.
 * The button only opens the shared, destructive `ConfirmModal` (mirrors
 * `LibraryDedupDialog` / `SourcelessCleanupDialog`), which alone emits `confirm`.
 * The confirm copy states the two things the owner needs to weigh: nothing is
 * deleted, and it will take roughly N cycles against one source.
 *
 * 🔴 It cannot tell which files are actually damaged, and does not try. Image
 * scrambling is a permutation of tiles, so it preserves every pixel, the
 * histogram, the dimensions and the edge count — every cheap image statistic is
 * permutation-invariant and blind to it. The filter re-downloads the lot.
 *
 * GOTCHA: the scanlator field is PRESENCE-based, not emptiness-based, because a
 * provider is a (source, scanlator) pair. Leaving the "just one scanlator" box
 * unticked matches EVERY scanlator of the source; ticking it and leaving the
 * field blank matches the source's all-scanlators provider SPECIFICALLY. Those
 * are different sets, which is why a bare text field could not express both.
 *
 * GOTCHA: the filter is NOT idempotent across runs. It selects on the time the CBZ
 * was last WRITTEN, and a successful re-download rewrites that time to now — so the
 * chapters this sweep fixes still match the same filter afterwards. Re-running it
 * unchanged re-downloads them all over again. The hint under the cutoff field says
 * so; that sentence is the only warning the owner gets, so do not trim it.
 *
 * The cutoff is entered in LOCAL time (a native datetime-local control) and
 * emitted as a UTC RFC 3339 instant, since the API compares it against a stored
 * timestamp.
 *
 * Presentation-only: the parent owns both requests and passes the §16 state down.
 */
const props = withDefaults(defineProps<{
  /** The server's answer to the last "Check", or null when none is loaded. */
  preview?: RedownloadPreview | null
  /** True while the preview request is in flight. */
  previewBusy?: boolean
  /** A failed-preview message, or null for none. */
  previewError?: string | null
  /** True while the apply request is in flight — spins confirm, disables the button. */
  applying?: boolean
  /** Success line from the last apply, or null. */
  applyMessage?: string | null
  /** Failure line from the last apply, or null. */
  applyError?: string | null
}>(), {
  preview: null,
  previewBusy: false,
  previewError: null,
  applying: false,
  applyMessage: null,
  applyError: null,
})

const emit = defineEmits<{
  /** Load the preview for this filter — reads only, never mutates. */
  preview: [filter: RedownloadFilter]
  /** Apply the filter — reachable ONLY via the ConfirmModal. */
  confirm: [filter: RedownloadFilter]
  /** The filter changed, so any loaded preview/outcome is now stale. */
  reset: []
}>()

const source = ref('')
/** Whether the owner is narrowing to ONE scanlator (the presence half). */
const narrowScanlator = ref(false)
const scanlator = ref('')
/** The cutoff as the native datetime-local control renders it (LOCAL time). */
const sinceLocal = ref('')

/**
 * The cutoff as a UTC RFC 3339 instant, or null when it is missing/unparseable.
 * `datetime-local` yields no timezone, so the browser's own zone is applied —
 * which is what the owner means by "since 09:39 this morning".
 */
const sinceIso = computed<string | null>(() => {
  if (!sinceLocal.value) return null
  const parsed = new Date(sinceLocal.value)
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString()
})

/** Both mandatory fields present — the server rejects anything less. */
const complete = computed(() => source.value.trim() !== '' && sinceIso.value !== null)

/** The filter as the API takes it, or null while incomplete. */
const filter = computed<RedownloadFilter | null>(() => {
  if (!complete.value || sinceIso.value === null) return null
  return {
    source: source.value.trim(),
    scanlator: narrowScanlator.value ? scanlator.value : null,
    since: sinceIso.value,
  }
})

// Any edit invalidates the loaded preview: a count that belongs to a filter the
// owner has since changed is worse than no count at all.
watch([source, narrowScanlator, scanlator, sinceLocal], () => emit('reset'))

/** Whether the QCAT-222 confirm gate is showing. */
const confirming = ref(false)

// Close the gate once the apply finishes, so the success/failure line is read
// against the pane rather than behind a still-open modal.
watch(() => props.applying, (isApplying, wasApplying) => {
  if (wasApplying && !isApplying) confirming.value = false
})

const matched = computed(() => props.preview?.matched ?? 0)

/** The honest cost line, or null when the server could not resolve the batch size. */
const costLine = computed<string | null>(() => {
  const p = props.preview
  if (!p || p.matched === 0 || p.perCycle < 1) return null
  return `Roughly ${p.estimatedCycles} download cycle${p.estimatedCycles === 1 ? '' : 's'} `
    + `at ${p.perCycle} chapters per cycle against this one source. Download speed is the anti-block throttle and is not raised for this.`
})

function onCheck(): void {
  if (!filter.value || props.previewBusy) return
  emit('preview', filter.value)
}

/** Opens the confirm gate — never re-downloads anything by itself. */
function requestConfirm(): void {
  if (matched.value === 0 || props.applying) return
  confirming.value = true
}

/** Only reachable from the ConfirmModal's own confirm button. */
function onConfirmed(): void {
  if (filter.value) emit('confirm', filter.value)
}

const confirmTitle = computed(() => `Re-download ${matched.value} chapter${matched.value === 1 ? '' : 's'}?`)
const confirmMessage = computed(() =>
  `These chapters will be fetched again from ${source.value.trim()}. Nothing is deleted — each existing file stays on disk `
  + `and readable until its replacement lands, so a failed re-download loses nothing. `
  + (costLine.value ?? ''),
)
</script>

<template>
  <div class="redl">
    <TextField
      v-model="source"
      label="Source"
      placeholder="Comix"
      :disabled="applying"
    />
    <p class="redl__hint">The source name exactly as Source Health lists it.</p>

    <label class="redl__narrow">
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). Same footgun as Dialog.vue. -->
      <Checkbox v-model="narrowScanlator" :disabled="applying" :ariaLabel="'Narrow to one scanlator'" />
      <span>Just one scanlator</span>
    </label>
    <TextField
      v-if="narrowScanlator"
      v-model="scanlator"
      label="Scanlator"
      placeholder="Valir Scans"
      :disabled="applying"
    />
    <p v-if="narrowScanlator" class="redl__hint">
      Leave blank to target the source's all-scanlators entry specifically.
    </p>

    <TextField
      v-model="sinceLocal"
      label="Downloaded since"
      type="datetime-local"
      :disabled="applying"
    />
    <p class="redl__hint">
      Matches chapters whose file was WRITTEN at or after this time, so a chapter downloaded
      earlier and later replaced by a better source is included. A successful re-download
      updates that time to now, so running the same filter again re-queues the chapters it
      already fixed — move the cutoff forward, or expect to pay for them twice.
    </p>

    <div class="redl__actions">
      <AppButton variant="ghost" :disabled="!complete || previewBusy || applying" @click="onCheck">
        {{ previewBusy ? 'Checking…' : 'Check' }}
      </AppButton>
      <AppButton
        v-if="preview"
        :disabled="matched === 0 || applying"
        @click="requestConfirm"
      >
        Re-download {{ matched }} chapter{{ matched === 1 ? '' : 's' }}
      </AppButton>
    </div>

    <p v-if="preview" class="redl__msg">
      {{ matched }} chapter{{ matched === 1 ? '' : 's' }} match this filter.
    </p>
    <p v-if="costLine" class="redl__notice">{{ costLine }}</p>
    <p v-if="applyMessage" class="redl__msg">{{ applyMessage }}</p>
    <p v-if="previewError" class="redl__err">{{ previewError }}</p>
    <p v-if="applyError" class="redl__err">{{ applyError }}</p>

    <!-- QCAT-222: the sweep can ONLY be started from this shared confirm gate,
         never straight from the button above. -->
    <ConfirmModal
      :open="confirming"
      :busy="applying"
      :title="confirmTitle"
      :message="confirmMessage"
      confirm-label="Re-download"
      destructive
      @update:open="confirming = $event"
      @confirm="onConfirmed"
    />
  </div>
</template>

<style scoped>
.redl {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: stretch;
  padding: 4px 0;
}

.redl__hint {
  margin: -4px 0 4px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--muted);
}

.redl__narrow {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  color: var(--muted);
  cursor: pointer;
}

.redl__actions {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 4px;
}

.redl__msg {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}

/* Amber, not rose: the cycle cost is not a failure, it is the price of the
   anti-block throttle. Reuses the Settings screen's existing attention amber. */
.redl__notice {
  margin: 0;
  font-size: 12px;
  color: var(--set-update-text);
}

.redl__err {
  margin: 0;
  font-size: 12px;
  color: var(--danger-text);
}
</style>
