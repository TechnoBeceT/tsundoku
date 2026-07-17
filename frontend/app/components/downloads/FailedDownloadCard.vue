<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ChapterDownloadRow from './ChapterDownloadRow.vue'
import type { DownloadItem, ErrorCategory } from '../screens/downloads.types'

/**
 * FailedDownloadCard — the failed-tab row variant: a (bare) ChapterDownloadRow
 * carrying the retry-count + next-attempt before its badge and a retry/reset
 * button after it, plus an expandable last-error panel below.
 *
 * The retry button surfaces its in-flight state (§16): while `retrying` it spins
 * and reads "Retrying…". Terminal (`permanently_failed`) rows label the action
 * "Reset" instead of "Retry". The error panel's expansion is owner-controlled —
 * the parent keeps the single-open `expanded` flag and handles `toggle-expand`
 * so only one card opens at a time.
 */
const props = defineProps<{
  /** The failed/terminal chapter-activity item. */
  item: DownloadItem
  /** This row's retry is in flight — spins + disables the button (§16). */
  retrying?: boolean
  /** Whether the last-error detail panel is expanded. */
  expanded?: boolean
}>()

const emit = defineEmits<{
  /** The cover/title was clicked — open that series. */
  'open-series': [seriesId: string]
  /** Retry (or reset) this chapter — carries its chapter id. */
  'retry': [chapterId: string]
  /** The error toggle was clicked — flip this card's expansion. */
  'toggle-expand': []
}>()

/** The error category a failed chapter carries → a human-readable label. */
const ERROR_LABELS: Record<ErrorCategory, string> = {
  network: 'Network error',
  source: 'Source error',
  cloudflare: 'Cloudflare block',
  timeout: 'Timed out',
  parse: 'Parse error',
}

// Terminal rows "Reset", retryable rows "Retry".
const retryLabel = computed(() => (props.item.state === 'permanently_failed' ? 'Reset' : 'Retry'))
const hasRetries = computed(() => (props.item.retries ?? 0) > 0)
const errorLabel = computed(() => (props.item.errorCategory ? ERROR_LABELS[props.item.errorCategory] : 'Error'))
</script>

<template>
  <div class="dl-card">
    <ChapterDownloadRow bare :item="item" @open-series="emit('open-series', $event)">
      <template #before-badge>
        <span v-if="hasRetries" class="retry-badge">Retry #{{ item.retries }}</span>
        <span v-if="item.nextAttempt" class="next-attempt">{{ item.nextAttempt }}</span>
      </template>
      <template #after-badge>
        <AppButton variant="mini" size="sm" :loading="retrying" @click="emit('retry', item.chapterId)">
          {{ retrying ? 'Retrying…' : retryLabel }}
        </AppButton>
      </template>
    </ChapterDownloadRow>

    <div v-if="item.lastError" class="dl-card__error">
      <button type="button" class="err-toggle" @click="emit('toggle-expand')">
        <span class="err-toggle__label">{{ errorLabel }}</span>
        <span class="err-toggle__msg">{{ item.lastError }}</span>
      </button>
      <div v-if="expanded" class="err-panel">
        <div class="err-panel__msg">{{ item.lastError }}</div>
        <div>category: {{ errorLabel }} · retries: {{ item.retries ?? 0 }}<template v-if="item.nextAttempt"> · next attempt {{ item.nextAttempt }}</template></div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dl-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 0.6875rem var(--space-base); /* 11px 14px @16 (11px off-ladder) */
}

/* ---- Failed-row extras (before the badge) --------------------------------- */
.retry-badge {
  flex: none;
  font-size: 0.65625rem; /* 10.5px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-bold);
  padding: var(--space-3xs) var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--dl-queued-bg);
  color: var(--dl-queued-text);
}

.next-attempt {
  flex: none;
  font-size: var(--text-xs);
  color: var(--faint);
}

/* ---- Expandable last-error ------------------------------------------------ */
.dl-card__error {
  margin-top: 0.5625rem; /* 9px @16 — off-ladder, byte-identical rem literal */
  /* 53px = ChapterDownloadRow's cover (2.5rem) + row gap (0.8125rem), as rem so
     the error block stays aligned under the title as the root scales. */
  padding-left: 3.3125rem;
}

.err-toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.4375rem; /* 7px @16 — off-ladder, byte-identical rem literal */
  max-width: 100%;
  background: none;
  border: none;
  cursor: pointer;
  padding: 0;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  text-align: left;
}

.err-toggle__label {
  flex: none;
  font-weight: var(--weight-bold);
  padding: 0.0625rem var(--space-xs-tight); /* 1px 6px @16 (1px off-ladder) */
  border-radius: var(--radius-xs);
  background: var(--dl-error-pill-bg);
  color: var(--dl-failed-text);
}

.err-toggle__msg {
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.err-panel {
  margin-top: 0.5625rem; /* 9px @16 — off-ladder, byte-identical rem literal */
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0.6875rem 0.8125rem; /* 11px 13px @16 — off-ladder, byte-identical */
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
  line-height: 1.6;
}

.err-panel__msg {
  color: var(--dl-failed-text);
  margin-bottom: var(--space-xs-tight);
  word-break: break-word;
}
</style>
