<script setup lang="ts">
import { computed } from 'vue'
import ConfirmModal from '../ui/ConfirmModal.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import type { components } from '~/utils/api/schema.d.ts'

type SourcePurgePreview = components['schemas']['SourcePurgePreview']

/**
 * PurgeSourceDialog — the confirm prompt for PURGING one source: removing all of
 * Tsundoku's DB state for it (its dangling SeriesProviders + feeds, its metric +
 * circuit-breaker rows) while KEEPING every downloaded CBZ. A thin destructive
 * `ConfirmModal` wrapper (mirrors RemoveSourceDialog) that additionally shows a
 * dry-run PREVIEW of the blast radius so the owner sees what will be removed
 * before committing. Controlled via `v-model:open`; `busy` spins confirm + blocks
 * dismissal.
 *
 * §16: a FAILED purge keeps the dialog open and shows the reason inside it
 * (`error`) — the owner never confirms into the void. The parent closes the
 * dialog only once the purge actually succeeded.
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `sourceName`: the source name shown in the heading.
 *   - `preview`: the dry-run counts (null while loading).
 *   - `previewing`: the preview fetch is in flight.
 *   - `busy`: the purge is in flight (spins confirm + blocks dismissal).
 *   - `error`: a failed-purge (or failed-preview) message, or null.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The source name, shown in the heading. May be "" when it can't be resolved. */
  sourceName: string
  /** The dry-run blast-radius counts, or null while loading. */
  preview?: SourcePurgePreview | null
  /** Whether the preview fetch is in flight. */
  previewing?: boolean
  /** In-flight flag — spins confirm + blocks dismissal. */
  busy?: boolean
  /** A failed-purge/preview message to show inside the dialog, or null. */
  error?: string | null
}>(), {
  preview: null,
  previewing: false,
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The purge was confirmed. */
  'confirm': []
}>()

// Never render an empty quoted name: fall back to a generic heading when the
// source name can't be resolved.
const title = computed(() =>
  props.sourceName.trim() ? `Purge “${props.sourceName}”?` : 'Purge this source?',
)
</script>

<template>
  <ConfirmModal
    :open="open"
    :busy="busy"
    :title="title"
    message="This removes all of Tsundoku's tracking data for this source. Downloaded CBZ files and chapters are KEPT — only the source's provider links, metrics, and breaker state are deleted."
    confirm-label="Purge source"
    destructive
    @update:open="emit('update:open', $event)"
    @confirm="emit('confirm')"
  >
    <p v-if="previewing" class="purge__loading">Checking what will be removed…</p>
    <ul v-else-if="preview" class="purge__preview">
      <li><b>{{ preview.seriesAffected }}</b> series affected</li>
      <li><b>{{ preview.providers }}</b> source link(s) removed</li>
      <li><b>{{ preview.providerChapters }}</b> tracked chapter(s) in this source's feed</li>
      <li v-if="preview.chaptersDeleted > 0">
        <b>{{ preview.chaptersDeleted }}</b> orphaned chapter(s) removed <span class="purge__hint">(no file + no other source)</span>
      </li>
      <li><b>{{ preview.metrics }}</b> metric + <b>{{ preview.breaker }}</b> breaker row(s)</li>
    </ul>

    <ErrorBanner v-if="error" class="purge__error" :message="error" :dismissible="false" />
  </ConfirmModal>
</template>

<style scoped>
.purge__loading {
  margin: 0 0 12px;
  font-size: var(--text-sm);
  color: var(--muted);
}

.purge__preview {
  margin: 0 0 12px;
  padding: 0 0 0 2px;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: var(--text-sm);
  color: var(--text);
}

.purge__preview b {
  font-family: var(--font-mono);
  font-weight: var(--weight-bold);
}

.purge__hint {
  color: var(--muted);
  font-size: var(--text-xs);
}

.purge__error {
  margin-bottom: 4px;
}
</style>
