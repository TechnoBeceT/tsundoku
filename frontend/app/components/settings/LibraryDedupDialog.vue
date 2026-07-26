<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ConfirmModal from '../ui/ConfirmModal.vue'

/**
 * LibraryDedupDialog — the trigger + confirm gate for the LIBRARY-WIDE duplicate
 * source clean-up (`POST /api/library/dedup-providers`). A disk import records a
 * source by its display NAME while a live attach records its numeric id, so the
 * same physical source can end up as two rows on one series; this folds every such
 * pair back into one without re-downloading anything.
 *
 * 🔴 QCAT-222: the sweep mutates the whole library — it RENAMES CBZ files to the
 * surviving source's identity and removes the drained duplicate source row — so
 * the trigger button MUST NOT fire it directly. The button only opens the shared,
 * destructive `ConfirmModal` (mirrors `PurgeSourceDialog` /
 * `SourcelessCleanupDialog`), which alone emits `confirm`. No CBZ is deleted, but
 * a bulk rename across the library still deserves a deliberate confirmation.
 *
 * Presentation-only: the parent owns the request and passes the §16 trio down.
 *
 *   - `busy`: the trigger request is in flight — spins confirm, disables the button.
 *   - `message`: the started/success line shown under the button, or null.
 *   - `error`: the failure line shown under the button, or null.
 *   - `skippedBusy`: how many series the finished sweep had to skip because
 *     another merge was already running on them. Rendered as its own actionable
 *     line ("run it again to catch them") rather than folded into `message` —
 *     the owner reacts to it differently from the empty-feed skips the summary
 *     mentions, and a count hidden inside a sentence is a count nobody acts on.
 *
 * Emits `confirm` — and only from the ConfirmModal's own confirm button.
 */
import { busyNotice } from '~/utils/dedupSweepSummary'

const props = withDefaults(defineProps<{
  /** True while the trigger request is in flight. */
  busy?: boolean
  /** Started/success message from the last trigger, or null. */
  message?: string | null
  /** Failure message from the last trigger, or null. */
  error?: string | null
  /** Series the last sweep skipped because a merge was already running (0 = none). */
  skippedBusy?: number
}>(), {
  busy: false,
  message: null,
  error: null,
  skippedBusy: 0,
})

/** The actionable "run it again" line, or null when nothing was skipped. */
const busyLine = computed(() => busyNotice(props.skippedBusy))

const emit = defineEmits<{
  /** The sweep was confirmed — reachable ONLY via the ConfirmModal. */
  confirm: []
}>()

/** Whether the QCAT-222 confirm gate is showing. */
const confirming = ref(false)

// Close the gate once the request finishes, so a success/failure line is read
// against the pane rather than behind a still-open modal.
watch(() => props.busy, (isBusy, wasBusy) => {
  if (wasBusy && !isBusy) confirming.value = false
})

/** Opens the confirm gate — never starts the sweep by itself. */
function requestConfirm(): void {
  if (props.busy) return
  confirming.value = true
}
</script>

<template>
  <div class="dedup">
    <AppButton :disabled="busy" @click="requestConfirm">
      {{ busy ? 'Starting…' : 'Clean up duplicate sources (library-wide)' }}
    </AppButton>
    <p v-if="message" class="dedup__msg">{{ message }}</p>
    <p v-if="busyLine" class="dedup__notice">{{ busyLine }}</p>
    <p v-if="error" class="dedup__err">{{ error }}</p>

    <!-- QCAT-222: the sweep can ONLY be started from this shared destructive
         confirm gate, never straight from the button above. -->
    <ConfirmModal
      :open="confirming"
      :busy="busy"
      title="Clean up duplicate sources?"
      message="Every series carrying the same physical source twice is folded into one source. CBZ files are RENAMED to the surviving source's identity and KEPT — nothing is deleted and nothing is re-downloaded. This runs across your whole library and cannot be undone from the app."
      confirm-label="Clean up library"
      destructive
      @update:open="confirming = $event"
      @confirm="emit('confirm')"
    />
  </div>
</template>

<style scoped>
.dedup {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
  padding: 4px 0;
}

.dedup__msg {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}

/* Amber, not rose: series skipped for being busy are not a failure — they are
   work still to do. Reuses the Settings screen's existing attention amber. */
.dedup__notice {
  margin: 0;
  font-size: 12px;
  color: var(--set-update-text);
}

.dedup__err {
  margin: 0;
  font-size: 12px;
  color: var(--danger-text);
}
</style>
