<script setup lang="ts">
import { computed, ref } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import type { DownloadItem, DownloadState, DownloadTab, ErrorCategory, RetryAllState } from './downloads.types'

/**
 * Downloads — the cross-library download-activity screen. ONE screen, three tabs
 * (Active · Failed · Queued) that are filtered views over the same flat chapter
 * list, grouped by `Chapter.state`:
 *   - Active  → downloading / upgrading (indeterminate progress, no actions)
 *   - Failed  → failed / permanently_failed (per-row retry + bulk retry/reset)
 *   - Queued  → wanted / upgrade_available (upgrades-only toggle)
 *
 * Presentation only: ALL data arrives via props and every mutating action is
 * emitted — no fetching, routing, or stores. Search, the failed sub-tabs, the
 * upgrades-only toggle, error-row expansion, and the requeue-confirm modal are
 * pure local view state. Token-only colours → renders correctly in both themes.
 *
 * PHASE B: the per-row markup (cover · title+category · meta · state badge) is a
 * reusable `ChapterDownloadRow` waiting to be atomised out of this SFC.
 */
const props = withDefaults(defineProps<{
  /** The full cross-library activity list; the tabs derive their views from it. */
  items: DownloadItem[]
  /** Which top-level tab is active. */
  activeTab?: DownloadTab
  /** Whether a download cycle is currently running (SSE-driven). */
  cycleActive?: boolean
  /** Minutes until the next cycle, for the idle banner ("~14 min"); null hides it. */
  nextCycleMinutes?: number | null
  /** Chapter ids with a single retry in flight — disables that row + shows a spinner. */
  retryingIds?: string[]
  /** A bulk retry/reset in flight (its scope), or null when idle. */
  retryingAll?: RetryAllState | null
  /** A surfaced retry failure — rendered as a dismissible banner, never swallowed (§16). */
  retryError?: string
  /** When true, render skeleton rows instead of content. */
  loading?: boolean
}>(), {
  activeTab: 'active',
  cycleActive: false,
  nextCycleMinutes: null,
  retryingIds: () => [],
  retryingAll: null,
  retryError: '',
  loading: false,
})

const emit = defineEmits<{
  /** A top-level tab was selected. */
  'set-tab': [tab: DownloadTab]
  /** Retry a single chapter (failed → wanted). */
  'retry': [chapterId: string]
  /** Bulk-retry every chapter in the given terminal/retryable state. */
  'retry-all': [state: RetryAllState]
  /** A row was clicked — open that series' detail view. */
  'open-series': [seriesId: string]
  /** Dismiss the surfaced retry-error banner. */
  'dismiss-error': []
}>()

// ---- Local view state (presentation only, never round-trips) ----------------
const search = ref('')
const failSubTab = ref<'all' | 'retryable' | 'terminal'>('all')
const upgradesOnly = ref(false)
const expandedId = ref<string | null>(null)
const confirm = ref<{ state: RetryAllState, count: number } | null>(null)

// ---- State → badge mapping --------------------------------------------------
const BADGES: Record<DownloadState, { label: string, cls: string }> = {
  wanted: { label: 'Wanted', cls: 'badge--wanted' },
  downloading: { label: 'Downloading', cls: 'badge--downloading' },
  upgrading: { label: 'Upgrading', cls: 'badge--upgrading' },
  upgrade_available: { label: 'Upgrade ready', cls: 'badge--queued' },
  failed: { label: 'Failed', cls: 'badge--failed' },
  permanently_failed: { label: 'Failed · final', cls: 'badge--terminal' },
}

const ERROR_LABELS: Record<ErrorCategory, string> = {
  network: 'Network error',
  source: 'Source error',
  cloudflare: 'Cloudflare block',
  timeout: 'Timed out',
  parse: 'Parse error',
}

/** A display row: the raw item plus the small derived bits the template needs. */
interface DownloadRow extends DownloadItem {
  numberLabel: string
  badge: { label: string, cls: string }
  isUpgrade: boolean
  retryLabel: string
  hasRetries: boolean
  errorLabel: string
}

const toRow = (item: DownloadItem): DownloadRow => ({
  ...item,
  numberLabel: item.number == null ? '' : `#${item.number}`,
  badge: BADGES[item.state],
  isUpgrade: item.state === 'upgrade_available',
  retryLabel: item.state === 'permanently_failed' ? 'Reset' : 'Retry',
  hasRetries: (item.retries ?? 0) > 0,
  errorLabel: item.errorCategory ? ERROR_LABELS[item.errorCategory] : 'Error',
})

// The meta line under the title: "#147 · Chapter 147" (number dropped when null).
const metaLine = (row: DownloadRow): string =>
  [row.numberLabel, row.name].filter(Boolean).join(' · ')

// Whether a single chapter's retry is currently in flight (§16 in-flight state).
const isRetrying = (chapterId: string): boolean => props.retryingIds.includes(chapterId)

// ---- Filtering --------------------------------------------------------------
const byState = (states: DownloadState[]): DownloadItem[] =>
  props.items.filter((i) => states.includes(i.state))

const applySearch = (list: DownloadItem[]): DownloadItem[] => {
  const q = search.value.trim().toLowerCase()
  return q ? list.filter((i) => i.seriesTitle.toLowerCase().includes(q)) : list
}

// ---- Counts (drive the tab badges, always from the full unfiltered set) -----
const counts = computed(() => ({
  active: byState(['downloading', 'upgrading']).length,
  failed: byState(['failed']).length,
  terminal: byState(['permanently_failed']).length,
  queued: byState(['wanted', 'upgrade_available']).length,
}))

const mainTabs = computed(() => [
  { key: 'active' as const, label: 'Active', count: counts.value.active },
  { key: 'failed' as const, label: 'Failed', count: counts.value.failed + counts.value.terminal },
  { key: 'queued' as const, label: 'Queued', count: counts.value.queued },
])

const failTabs = computed(() => [
  { key: 'all' as const, label: 'All failures', n: counts.value.failed + counts.value.terminal },
  { key: 'retryable' as const, label: 'Retryable', n: counts.value.failed },
  { key: 'terminal' as const, label: 'Terminal', n: counts.value.terminal },
])

// ---- Per-tab rows -----------------------------------------------------------
const activeRows = computed(() => applySearch(byState(['downloading', 'upgrading'])).map(toRow))

const failedRows = computed(() => {
  const states: DownloadState[]
    = failSubTab.value === 'retryable'
      ? ['failed']
      : failSubTab.value === 'terminal'
        ? ['permanently_failed']
        : ['failed', 'permanently_failed']
  return applySearch(byState(states)).map(toRow)
})

const queuedRows = computed(() => {
  let list = byState(['wanted', 'upgrade_available'])
  if (upgradesOnly.value) list = list.filter((i) => i.state === 'upgrade_available')
  return applySearch(list).map(toRow)
})

// ---- Cycle banner -----------------------------------------------------------
const cycleLabel = computed(() =>
  props.cycleActive
    ? 'Download cycle in progress…'
    : props.nextCycleMinutes == null
      ? 'Idle — waiting for next cycle'
      : `Next download cycle ~${props.nextCycleMinutes} min`,
)

// ---- Actions ----------------------------------------------------------------
const selectTab = (tab: DownloadTab): void => {
  expandedId.value = null
  emit('set-tab', tab)
}

const toggleExpand = (id: string): void => {
  expandedId.value = expandedId.value === id ? null : id
}

// Open the requeue-confirm modal for a bulk action (no-op when there is nothing
// to requeue, mirroring the prototype's disabled bulk buttons).
const openConfirm = (state: RetryAllState, count: number): void => {
  if (count > 0) confirm.value = { state, count }
}

const confirmRequeue = (): void => {
  if (!confirm.value) return
  emit('retry-all', confirm.value.state)
  confirm.value = null
}

const skeletons = Array.from({ length: 5 }, (_, i) => i)
</script>

<template>
  <div class="downloads">
    <!-- Top-level tabs + cycle banner -->
    <div class="downloads__head">
      <button
        v-for="t in mainTabs"
        :key="t.key"
        type="button"
        class="tab"
        :class="{ 'tab--active': activeTab === t.key }"
        @click="selectTab(t.key)"
      >
        {{ t.label }}
        <span class="tab__count" :class="{ 'tab__count--active': activeTab === t.key }">{{ t.count }}</span>
      </button>

      <div class="cycle">
        <span v-if="cycleActive" class="cycle__spinner" aria-hidden="true" />
        <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--accentBright)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7v5l3 2" />
        </svg>
        {{ cycleLabel }}
      </div>
    </div>

    <!-- Loading skeletons -->
    <div v-if="loading" class="rows">
      <div v-for="n in skeletons" :key="n" class="skeleton-row" />
    </div>

    <!-- ===================== ACTIVE ===================== -->
    <template v-else-if="activeTab === 'active'">
      <div v-if="activeRows.length === 0" class="empty">
        <div class="empty__icon empty__icon--accent">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <path d="M7 10l5 5 5-5" />
            <path d="M12 15V3" />
          </svg>
        </div>
        <div class="empty__title">No active downloads</div>
        <div class="empty__sub">Waiting for the next download cycle.</div>
      </div>

      <div v-else class="rows">
        <div v-for="row in activeRows" :key="row.chapterId" class="dl-row">
          <button type="button" class="dl-row__cover" :aria-label="`Open ${row.seriesTitle}`" @click="emit('open-series', row.seriesId)">
            <img v-if="row.coverUrl" class="dl-row__img" :src="row.coverUrl" :alt="`${row.seriesTitle} cover`" loading="lazy">
            <span v-else class="dl-row__ph"><BrandMark :size="18" tone="inverse" /></span>
          </button>
          <button type="button" class="dl-row__info" @click="emit('open-series', row.seriesId)">
            <div class="dl-row__titleline">
              <span class="dl-row__title">{{ row.seriesTitle }}</span>
              <span class="cat-chip">{{ row.seriesCategory }}</span>
            </div>
            <div class="dl-row__meta">{{ metaLine(row) }} <span class="dl-row__provider">· {{ row.provider }}</span></div>
          </button>
          <div class="progress" aria-hidden="true"><div class="progress__bar" /></div>
          <span class="badge" :class="row.badge.cls"><span class="badge__dot" />{{ row.badge.label }}</span>
        </div>
      </div>
    </template>

    <!-- ===================== FAILED ===================== -->
    <template v-else-if="activeTab === 'failed'">
      <div class="subhead">
        <button
          v-for="t in failTabs"
          :key="t.key"
          type="button"
          class="tab"
          :class="{ 'tab--active': failSubTab === t.key }"
          @click="failSubTab = t.key"
        >
          {{ t.label }}
          <span class="tab__count" :class="{ 'tab__count--active': failSubTab === t.key }">{{ t.n }}</span>
        </button>
        <div class="subhead__actions">
          <button type="button" class="mini-btn" :disabled="counts.failed === 0 || retryingAll !== null" @click="openConfirm('failed', counts.failed)">
            <span v-if="retryingAll === 'failed'" class="btn-spinner" aria-hidden="true" />
            <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 12a9 9 0 1 1-2.6-6.4" />
              <path d="M21 3v6h-6" />
            </svg>
            {{ retryingAll === 'failed' ? 'Retrying…' : 'Retry all retryable' }}
          </button>
          <button type="button" class="mini-btn" :disabled="counts.terminal === 0 || retryingAll !== null" @click="openConfirm('permanently_failed', counts.terminal)">
            <span v-if="retryingAll === 'permanently_failed'" class="btn-spinner" aria-hidden="true" />
            {{ retryingAll === 'permanently_failed' ? 'Resetting…' : 'Reset all terminal' }}
          </button>
        </div>
      </div>

      <div class="searchbar">
        <input v-model="search" type="search" class="searchbar__input" placeholder="Search series…">
      </div>

      <!-- Surfaced retry failure — visible + dismissible, never a console-only error (§16). -->
      <div v-if="retryError" class="error-banner" role="alert">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          <path d="M12 9v4M12 17h.01" />
        </svg>
        <span class="error-banner__msg">{{ retryError }}</span>
        <button type="button" class="error-banner__close" aria-label="Dismiss error" @click="emit('dismiss-error')">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>

      <div v-if="failedRows.length === 0" class="empty">
        <div class="empty__icon empty__icon--ok">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M20 6L9 17l-5-5" />
          </svg>
        </div>
        <div class="empty__title">No failed downloads</div>
      </div>

      <div v-else class="rows">
        <div v-for="row in failedRows" :key="row.chapterId" class="dl-card">
          <div class="dl-row">
            <button type="button" class="dl-row__cover" :aria-label="`Open ${row.seriesTitle}`" @click="emit('open-series', row.seriesId)">
              <img v-if="row.coverUrl" class="dl-row__img" :src="row.coverUrl" :alt="`${row.seriesTitle} cover`" loading="lazy">
              <span v-else class="dl-row__ph"><BrandMark :size="18" tone="inverse" /></span>
            </button>
            <button type="button" class="dl-row__info" @click="emit('open-series', row.seriesId)">
              <div class="dl-row__titleline">
                <span class="dl-row__title">{{ row.seriesTitle }}</span>
                <span class="cat-chip">{{ row.seriesCategory }}</span>
              </div>
              <div class="dl-row__meta">{{ metaLine(row) }} <span class="dl-row__provider">· {{ row.provider }}</span></div>
            </button>
            <span v-if="row.hasRetries" class="retry-badge">Retry #{{ row.retries }}</span>
            <span v-if="row.nextAttempt" class="next-attempt">{{ row.nextAttempt }}</span>
            <span class="badge" :class="row.badge.cls"><span class="badge__dot" />{{ row.badge.label }}</span>
            <button type="button" class="row-btn" :disabled="isRetrying(row.chapterId)" @click="emit('retry', row.chapterId)">
              <span v-if="isRetrying(row.chapterId)" class="btn-spinner" aria-hidden="true" />
              {{ isRetrying(row.chapterId) ? 'Retrying…' : row.retryLabel }}
            </button>
          </div>

          <div v-if="row.lastError" class="dl-card__error">
            <button type="button" class="err-toggle" @click="toggleExpand(row.chapterId)">
              <span class="err-toggle__label">{{ row.errorLabel }}</span>
              <span class="err-toggle__msg">{{ row.lastError }}</span>
            </button>
            <div v-if="expandedId === row.chapterId" class="err-panel">
              <div class="err-panel__msg">{{ row.lastError }}</div>
              <div>category: {{ row.errorLabel }} · retries: {{ row.retries ?? 0 }}<template v-if="row.nextAttempt"> · next attempt {{ row.nextAttempt }}</template></div>
            </div>
          </div>
        </div>
      </div>
    </template>

    <!-- ===================== QUEUED ===================== -->
    <template v-else>
      <div class="queued-head">
        <div class="searchbar">
          <input v-model="search" type="search" class="searchbar__input" placeholder="Search series…">
        </div>
        <label class="toggle">
          <button
            type="button"
            class="switch"
            :class="{ 'switch--on': upgradesOnly }"
            role="switch"
            :aria-checked="upgradesOnly"
            @click="upgradesOnly = !upgradesOnly"
          >
            <span class="switch__knob" />
          </button>
          <span class="toggle__label">Upgrades only</span>
        </label>
      </div>

      <div v-if="queuedRows.length === 0" class="empty">
        <div class="empty__icon empty__icon--faint">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle cx="12" cy="12" r="9" />
            <path d="M12 7v5l3 2" />
          </svg>
        </div>
        <div class="empty__title">No chapters queued</div>
        <div class="empty__sub">Library is up to date.</div>
      </div>

      <div v-else class="rows">
        <div v-for="row in queuedRows" :key="row.chapterId" class="dl-row">
          <button type="button" class="dl-row__cover" :aria-label="`Open ${row.seriesTitle}`" @click="emit('open-series', row.seriesId)">
            <img v-if="row.coverUrl" class="dl-row__img" :src="row.coverUrl" :alt="`${row.seriesTitle} cover`" loading="lazy">
            <span v-else class="dl-row__ph"><BrandMark :size="18" tone="inverse" /></span>
          </button>
          <button type="button" class="dl-row__info" @click="emit('open-series', row.seriesId)">
            <div class="dl-row__titleline">
              <span class="dl-row__title">{{ row.seriesTitle }}</span>
              <span class="cat-chip">{{ row.seriesCategory }}</span>
            </div>
            <div class="dl-row__meta">{{ metaLine(row) }} <span class="dl-row__provider">· {{ row.provider }}</span></div>
          </button>
          <span v-if="row.isUpgrade" class="upgrade-tag">
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M12 19V5M5 12l7-7 7 7" />
            </svg>
            UPGRADE
          </span>
          <span class="badge" :class="row.badge.cls"><span class="badge__dot" />{{ row.badge.label }}</span>
        </div>
      </div>
    </template>

    <!-- Requeue-confirm modal -->
    <div v-if="confirm" class="modal">
      <div class="modal__card">
        <div class="modal__title">Requeue chapters?</div>
        <div class="modal__text">
          This will requeue {{ confirm.count }} chapter{{ confirm.count > 1 ? 's' : '' }}. They'll download on the next cycle. Files are never deleted.
        </div>
        <div class="modal__actions">
          <button type="button" class="ghost-btn" @click="confirm = null">Cancel</button>
          <button type="button" class="primary-btn" @click="confirmRequeue">Requeue</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.downloads {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* ---- Tab bar + cycle banner ----------------------------------------------- */
.downloads__head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 18px;
}

.tab {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 14px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.tab:hover {
  color: var(--text);
  border-color: var(--border2);
}

.tab--active {
  border-color: transparent;
  background: var(--accentSoft);
  color: var(--accentBright);
}

.tab__count {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--faint);
}

.tab__count--active {
  background: var(--accent);
  color: var(--cover-text);
}

.cycle {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 14px;
  border-radius: var(--radius-pill);
  background: var(--surface2);
  border: 1px solid var(--border);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.cycle__spinner {
  width: 11px;
  height: 11px;
  border: 2px solid var(--accentBright);
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: dl-spin 0.8s linear infinite;
}

/* ---- Failed sub-head + bulk actions --------------------------------------- */
.subhead {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}

.subhead__actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.mini-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 13px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.mini-btn:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accentBright);
}

.mini-btn:disabled {
  color: var(--faint);
  opacity: 0.5;
  cursor: default;
}

/* ---- Queued head ---------------------------------------------------------- */
.queued-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}

/* ---- Search --------------------------------------------------------------- */
.searchbar {
  width: 300px;
  max-width: 100%;
  margin-bottom: 14px;
}

.queued-head .searchbar {
  margin-bottom: 0;
}

.searchbar__input {
  width: 100%;
  padding: 9px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.searchbar__input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

/* ---- Upgrades-only toggle ------------------------------------------------- */
.toggle {
  display: flex;
  align-items: center;
  gap: 9px;
}

.toggle__label {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.switch {
  width: 44px;
  height: 25px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  border: 1px solid var(--border);
  position: relative;
  cursor: pointer;
  padding: 0;
  flex: none;
  transition: background 0.2s;
}

.switch--on {
  background: var(--accent);
}

.switch__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: var(--cover-text);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: left 0.2s;
}

.switch--on .switch__knob {
  left: 21px;
}

/* ---- Row list ------------------------------------------------------------- */
.rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.dl-row {
  display: flex;
  align-items: center;
  gap: 13px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 14px;
}

.dl-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 14px;
}

.dl-card .dl-row {
  background: none;
  border: none;
  border-radius: 0;
  padding: 0;
}

.dl-row__cover {
  width: 40px;
  height: 54px;
  border-radius: var(--radius-xs);
  overflow: hidden;
  position: relative;
  flex: none;
  padding: 0;
  border: none;
  cursor: pointer;
  background: var(--cover-placeholder);
}

.dl-row__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.dl-row__ph {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.dl-row__info {
  flex: 1;
  min-width: 0;
  text-align: left;
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
}

.dl-row__titleline {
  display: flex;
  align-items: center;
  gap: 8px;
}

.dl-row__title {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dl-row__meta {
  font-size: var(--text-sm);
  color: var(--muted);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dl-row__provider {
  color: var(--faint);
}

.cat-chip {
  flex: none;
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

/* ---- Indeterminate progress (active rows) --------------------------------- */
.progress {
  width: 90px;
  height: 5px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  overflow: hidden;
  flex: none;
}

.progress__bar {
  height: 100%;
  width: 42%;
  border-radius: var(--radius-pill);
  background: linear-gradient(90deg, var(--accent), var(--accentBright));
  animation: dl-slide 1.2s ease-in-out infinite;
}

/* ---- State badge ---------------------------------------------------------- */
.badge {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.badge__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-pill);
  flex-shrink: 0;
  background: currentColor;
}

.badge--wanted { color: var(--dl-wanted-text); background: var(--dl-wanted-bg); }
.badge--wanted .badge__dot { background: var(--dl-wanted-dot); }
.badge--downloading { color: var(--dl-downloading-text); background: var(--dl-downloading-bg); }
.badge--downloading .badge__dot { background: var(--dl-downloading-dot); }
.badge--upgrading { color: var(--dl-upgrading-text); background: var(--dl-upgrading-bg); }
.badge--upgrading .badge__dot { background: var(--dl-upgrading-dot); }
.badge--queued { color: var(--dl-queued-text); background: var(--dl-queued-bg); }
.badge--queued .badge__dot { background: var(--dl-queued-dot); }
.badge--failed { color: var(--dl-failed-text); background: var(--dl-failed-bg); }
.badge--failed .badge__dot { background: var(--dl-failed-dot); }
.badge--terminal { color: var(--dl-terminal-text); background: var(--dl-terminal-bg); }
.badge--terminal .badge__dot { background: var(--dl-terminal-dot); }

/* ---- Failed-row extras ---------------------------------------------------- */
.retry-badge {
  flex: none;
  font-size: 10.5px;
  font-weight: var(--weight-bold);
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--dl-queued-bg);
  color: var(--dl-queued-text);
}

.next-attempt {
  flex: none;
  font-size: var(--text-xs);
  color: var(--faint);
}

.row-btn {
  flex: none;
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 6px 12px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.row-btn:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accentBright);
}

.row-btn:disabled {
  color: var(--faint);
  opacity: 0.7;
  cursor: default;
}

/* In-button spinner (per-row retry + bulk retry/reset in-flight state, §16). */
.btn-spinner {
  width: 13px;
  height: 13px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: dl-spin 0.8s linear infinite;
}

/* ---- Surfaced retry-error banner (§16 — visible, dismissible) ------------- */
.error-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 14px;
  margin-bottom: 14px;
  border-radius: var(--radius-md);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
}

.error-banner__msg {
  flex: 1;
  min-width: 0;
}

.error-banner__close {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  background: none;
  border: none;
  color: var(--danger-text);
  cursor: pointer;
  transition: color 0.15s;
}

.error-banner__close:hover {
  color: var(--danger-bright);
}

/* ---- Expandable error ----------------------------------------------------- */
.dl-card__error {
  margin-top: 9px;
  padding-left: 53px;
}

.err-toggle {
  display: inline-flex;
  align-items: center;
  gap: 7px;
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
  padding: 1px 6px;
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
  margin-top: 9px;
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 11px 13px;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
  line-height: 1.6;
}

.err-panel__msg {
  color: var(--dl-failed-text);
  margin-bottom: 6px;
  word-break: break-word;
}

/* ---- Upgrade tag (queued) ------------------------------------------------- */
.upgrade-tag {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10.5px;
  font-weight: var(--weight-extrabold);
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--dl-queued-bg);
  color: var(--dl-queued-text);
}

/* ---- Empty states --------------------------------------------------------- */
.empty {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 50px;
  text-align: center;
}

.empty__icon {
  width: 48px;
  height: 48px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto 14px;
}

.empty__icon--accent {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.empty__icon--ok {
  background: var(--dl-ok-bg);
  color: var(--dl-ok-icon);
}

.empty__icon--faint {
  background: var(--surface3);
  color: var(--faint);
}

.empty__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
}

.empty__sub {
  font-size: var(--text-base);
  color: var(--muted);
  margin-top: 4px;
}

/* ---- Skeletons ------------------------------------------------------------ */
.skeleton-row {
  height: 76px;
  border-radius: var(--radius-lg);
  background: var(--surface2);
  position: relative;
  overflow: hidden;
}

.skeleton-row::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: dl-shimmer 1.4s ease-in-out infinite;
}

/* ---- Confirm modal -------------------------------------------------------- */
.modal {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: var(--cover-scrim);
}

.modal__card {
  width: 100%;
  max-width: 400px;
  background: var(--surface);
  border: 1px solid var(--border2);
  border-radius: var(--radius-2xl);
  padding: 22px;
  box-shadow: var(--shadow);
}

.modal__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-xl);
  color: var(--text);
  margin-bottom: 6px;
}

.modal__text {
  font-size: var(--text-base);
  color: var(--muted);
  margin-bottom: 20px;
  line-height: 1.5;
}

.modal__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.ghost-btn {
  padding: 10px 16px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: transparent;
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.primary-btn {
  padding: 10px 18px;
  border-radius: var(--radius-md);
  border: none;
  background: var(--accent);
  color: var(--cover-text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

/* ---- Keyframes ------------------------------------------------------------ */
@keyframes dl-spin {
  to { transform: rotate(360deg); }
}

@keyframes dl-slide {
  0% { transform: translateX(-120%); }
  100% { transform: translateX(320%); }
}

@keyframes dl-shimmer {
  to { transform: translateX(100%); }
}
</style>
