<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import AttemptBadge from './AttemptBadge.vue'
import ChapterDownloadRow from './ChapterDownloadRow.vue'
import type { DownloadItem } from '../screens/downloads.types'

/**
 * FailedDownloadCard — the failed-tab row variant: a (bare) ChapterDownloadRow
 * carrying the FAILING source's attempt badge + next-attempt before its badge and
 * a retry/reset button after it, plus an expandable last-error panel below.
 *
 * HONEST FAILURES: the badge + error name the source ACTUALLY failing this chapter
 * (`failing*` fields), NOT the satisfier. For a downloaded chapter whose UPGRADE
 * keeps failing those are different sources — the row reads e.g. "Comix → Hive
 * Scans · 5/5 · broken pages" (the meta's Upgrade→target comes from
 * ChapterDownloadRow via `isUpgrade`). A plain failed-state row falls back to its
 * own provider/attempts/lastError.
 *
 * The retry button surfaces its in-flight state (§16): while `retrying` it spins
 * and reads "Retrying…". Terminal rows label the action "Reset" instead of "Retry".
 * The error panel's expansion is owner-controlled — the parent keeps the
 * single-open `expanded` flag and handles `toggle-expand`.
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

/**
 * Error-category → human label. Covers BOTH the frontend `ErrorCategory` set and
 * the wider backend `failingErrorCategory` taxonomy (not_found, no_pages, …);
 * unknown values fall back to a title-cased form, then "Error".
 */
const CATEGORY_LABELS: Record<string, string> = {
  network: 'Network error',
  source: 'Source error',
  cloudflare: 'Cloudflare block',
  timeout: 'Timed out',
  parse: 'Parse error',
  captcha: 'Captcha / block',
  rate_limit: 'Rate limited',
  not_found: 'Not found',
  server_error: 'Server error',
  no_pages: 'No pages',
  unknown: 'Error',
}

// The FAILING source (broken-upgrade target for a downloaded row) drives the badge;
// fall back to the row's own satisfying source for a plain failed-state chapter.
const badgeProvider = computed(() => props.item.failingProviderName ?? props.item.providerName)
const badgeAttempts = computed(() =>
  props.item.failingProviderName ? (props.item.failingAttempts ?? 0) : (props.item.attempts ?? 0),
)
const badgeMax = computed(() => props.item.maxRetries ?? 0)
const showBadge = computed(() => badgeMax.value > 0)

// Terminal (budget spent) rows "Reset"; retryable rows "Retry". Terminal is the
// backend flag OR a permanently_failed chapter state.
const isTerminal = computed(() => props.item.terminal === true || props.item.state === 'permanently_failed')
const retryLabel = computed(() => (isTerminal.value ? 'Reset' : 'Retry'))

// Prefer the FAILING source's error over the chapter-level one (the honest cause).
const displayError = computed(() => props.item.failingLastError ?? props.item.lastError ?? '')
const displayCategory = computed(() => props.item.failingErrorCategory ?? props.item.errorCategory ?? '')
const errorLabel = computed(() => {
  const c = displayCategory.value
  if (!c) return 'Error'
  return CATEGORY_LABELS[c] ?? c.replace(/_/g, ' ').replace(/^\w/, (m) => m.toUpperCase())
})
</script>

<template>
  <div class="dl-card">
    <ChapterDownloadRow bare hide-attempts :item="item" @open-series="emit('open-series', $event)">
      <template #before-badge>
        <!-- The FAILING source's attempt/max badge (ChapterDownloadRow's own is
             suppressed via hide-attempts) + the scheduled next-attempt ETA. -->
        <AttemptBadge v-if="showBadge" :provider="badgeProvider" :attempts="badgeAttempts" :max="badgeMax" />
        <span v-if="item.nextAttempt" class="next-attempt">{{ item.nextAttempt }}</span>
      </template>
      <template #after-badge>
        <AppButton variant="mini" size="sm" :loading="retrying" @click="emit('retry', item.chapterId)">
          {{ retrying ? 'Retrying…' : retryLabel }}
        </AppButton>
      </template>
    </ChapterDownloadRow>

    <div v-if="displayError" class="dl-card__error">
      <button type="button" class="err-toggle" @click="emit('toggle-expand')">
        <span class="err-toggle__label">{{ errorLabel }}</span>
        <span class="err-toggle__msg">{{ displayError }}</span>
      </button>
      <div v-if="expanded" class="err-panel">
        <div class="err-panel__msg">{{ displayError }}</div>
        <div>source: {{ badgeProvider || '—' }} · category: {{ errorLabel }} · attempts: {{ badgeAttempts }}/{{ badgeMax }}<template v-if="item.nextAttempt"> · next attempt {{ item.nextAttempt }}</template></div>
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
