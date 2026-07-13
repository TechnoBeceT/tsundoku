<script setup lang="ts">
import { computed, ref } from 'vue'
import AppButton from '../ui/AppButton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import FormError from '../ui/FormError.vue'
import SearchInput from '../ui/SearchInput.vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import ProgressBar from '../ui/ProgressBar.vue'
import Skeleton from '../ui/Skeleton.vue'
import Toggle from '../ui/Toggle.vue'
import ChapterDownloadRow from '../downloads/ChapterDownloadRow.vue'
import CycleBanner from '../downloads/CycleBanner.vue'
import FailedDownloadCard from '../downloads/FailedDownloadCard.vue'
import RequeueConfirmModal from '../downloads/RequeueConfirmModal.vue'
import type { DownloadItem, DownloadState, DownloadTab, RetryAllState } from './downloads.types'

/**
 * Downloads — the cross-library download-activity screen. ONE screen, three tabs
 * (Active · Failed · Queued) that are filtered views over the same flat chapter
 * list, grouped by `Chapter.state`:
 *   - Active  → downloading / upgrading (indeterminate progress, no actions)
 *   - Failed  → failed / permanently_failed (per-row retry + bulk retry/reset)
 *   - Queued  → wanted / upgrade_available (upgrades-only toggle)
 *
 * Thin container: ALL data arrives via props and every mutating action is
 * emitted — no fetching, routing, or stores. The per-row markup, cycle banner,
 * and requeue prompt are atomised into `components/downloads/*`; search, the
 * failed sub-tabs, the upgrades-only toggle, error-row expansion, and the
 * confirm modal are pure local view state. Token-only → both themes render.
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
  /**
   * Exact per-state server counts for tab badges + bulk-action gating. Defaulted to zeros
   * so existing Storybook stories that omit this prop still render without errors.
   */
  counts?: { active: number; failed: number; terminal: number; queued: number }
  /** Server total for the active tab — drives the load-more affordance. */
  total?: number
  /** Whether more pages exist for the active tab (items.length < server total). */
  hasMore?: boolean
  /** Whether a load-more fetch is in flight — disables the load-more button. */
  loadingMore?: boolean
  /** Whether the manual "Download now" trigger is in flight (§16 busy state). */
  running?: boolean
  /** The last "Download now" success note (e.g. "Download cycle started"). */
  runMessage?: string
  /** The last "Download now" failure message, surfaced inline + never swallowed (§16). */
  runError?: string
}>(), {
  activeTab: 'active',
  cycleActive: false,
  nextCycleMinutes: null,
  retryingIds: () => [],
  retryingAll: null,
  retryError: '',
  loading: false,
  counts: () => ({ active: 0, failed: 0, terminal: 0, queued: 0 }),
  total: 0,
  hasMore: false,
  loadingMore: false,
  running: false,
  runMessage: '',
  runError: '',
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
  /** Load the next page of the active tab and append the results. */
  'load-more': []
  /** Trigger an immediate download cycle ("Download now"). */
  'run-now': []
}>()

// ---- Local view state (presentation only, never round-trips) ----------------
const search = ref('')
const failSubTab = ref<'all' | 'retryable' | 'terminal'>('all')
const upgradesOnly = ref(false)
const expandedId = ref<string | null>(null)
const confirm = ref<{ state: RetryAllState, count: number } | null>(null)

// Whether a single chapter's retry is currently in flight (§16 in-flight state).
const isRetrying = (chapterId: string): boolean => props.retryingIds.includes(chapterId)

// ---- Filtering --------------------------------------------------------------
const byState = (states: DownloadState[]): DownloadItem[] =>
  props.items.filter((i) => states.includes(i.state))

const applySearch = (list: DownloadItem[]): DownloadItem[] => {
  const q = search.value.trim().toLowerCase()
  return q ? list.filter((i) => i.seriesTitle.toLowerCase().includes(q)) : list
}

// ---- Counts (exact server totals received via props) ------------------------
// Computed alias so the template + mainTabs/failTabs accesses (counts.value.x /
// counts.x) continue to work without further change throughout the file.
const counts = computed(() => props.counts)

const mainTabs = computed(() => [
  { key: 'active', label: 'Active', count: counts.value.active },
  { key: 'failed', label: 'Failed', count: counts.value.failed + counts.value.terminal },
  { key: 'queued', label: 'Queued', count: counts.value.queued },
])

const failTabs = computed(() => [
  { key: 'all', label: 'All failures', count: counts.value.failed + counts.value.terminal },
  { key: 'retryable', label: 'Retryable', count: counts.value.failed },
  { key: 'terminal', label: 'Terminal', count: counts.value.terminal },
])

// ---- Per-tab rows -----------------------------------------------------------
const activeRows = computed(() => applySearch(byState(['downloading', 'upgrading'])))

const failedRows = computed(() => {
  const states: DownloadState[]
    = failSubTab.value === 'retryable'
      ? ['failed']
      : failSubTab.value === 'terminal'
        ? ['permanently_failed']
        : ['failed', 'permanently_failed']
  return applySearch(byState(states))
})

const queuedRows = computed(() => {
  let list = byState(['wanted', 'upgrade_available'])
  if (upgradesOnly.value) list = list.filter((i) => i.state === 'upgrade_available')
  return applySearch(list)
})

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
    <!-- QCAT-231 "fit the screen, scroll inside": everything down to here is the
         FLOWING top (tabs/cycle/run-now + the §16 result line) — short and
         fixed-content, so it never needs to scroll and is never clipped. Only
         `.downloads__body` below is bounded to the remaining viewport; see its
         comment. -->
    <div class="downloads__top">
      <!-- Top-level tabs + cycle banner + manual "Download now" trigger -->
      <div class="downloads__head">
        <SegmentedTabs :model-value="activeTab" :tabs="mainTabs" @update:model-value="selectTab($event as DownloadTab)" />
        <CycleBanner class="downloads__cycle" :cycle-active="cycleActive" :next-cycle-minutes="nextCycleMinutes" />
        <AppButton variant="mini" size="sm" :loading="running" @click="emit('run-now')">
          <template #icon>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 12a9 9 0 1 1-2.6-6.4" />
              <path d="M21 3v6h-6" />
            </svg>
          </template>
          {{ running ? 'Starting…' : 'Download now' }}
        </AppButton>
      </div>

      <!-- §16 "Download now" result: success note or failure, never swallowed. -->
      <p v-if="runMessage" class="run-note">{{ runMessage }}</p>
      <FormError v-if="runError" class="run-error" :message="runError" />
    </div>

    <!-- QCAT-231 bounded region: fits the remaining viewport under `.downloads__top`.
         Each tab's own toolbar (sub-tabs/search/toggle) stays fixed at the top of
         this region (`.downloads__toolbar`) and only the row list itself
         (`.downloads__list`) scrolls internally — so a long Failed/Queued list never
         forces the whole page to scroll, and the toolbar above it is always in reach. -->
    <div class="downloads__body">
      <!-- Loading skeletons -->
      <div v-if="loading" class="downloads__list">
        <div class="rows">
          <Skeleton v-for="n in skeletons" :key="n" variant="row" height="76px" />
        </div>
      </div>

      <!-- ===================== ACTIVE ===================== -->
      <div v-else-if="activeTab === 'active'" class="downloads__list">
        <EmptyState
          v-if="activeRows.length === 0"
          title="No active downloads"
          sub="Waiting for the next download cycle."
          icon-tone="accentBright"
        >
          <template #icon>
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <path d="M7 10l5 5 5-5" />
              <path d="M12 15V3" />
            </svg>
          </template>
        </EmptyState>

        <div v-else class="rows">
          <ChapterDownloadRow
            v-for="row in activeRows"
            :key="row.chapterId"
            :item="row"
            @open-series="emit('open-series', $event)"
          >
            <template #before-badge>
              <div class="downloads__progress">
                <ProgressBar :value="row.progress" tone="linear-gradient(90deg, var(--accent), var(--accentBright))" />
                <span v-if="row.pagesTotal" class="downloads__pages">{{ row.pagesCurrent ?? 0 }} / {{ row.pagesTotal }}</span>
              </div>
            </template>
          </ChapterDownloadRow>
        </div>
      </div>

      <!-- ===================== FAILED ===================== -->
      <template v-else-if="activeTab === 'failed'">
        <div class="downloads__toolbar">
          <div class="subhead">
            <SegmentedTabs
              :model-value="failSubTab"
              :tabs="failTabs"
              @update:model-value="failSubTab = $event as 'all' | 'retryable' | 'terminal'"
            />
            <div class="subhead__actions">
              <AppButton
                variant="mini"
                size="sm"
                :loading="retryingAll === 'failed'"
                :disabled="counts.failed === 0 || retryingAll !== null"
                @click="openConfirm('failed', counts.failed)"
              >
                <template #icon>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M21 12a9 9 0 1 1-2.6-6.4" />
                    <path d="M21 3v6h-6" />
                  </svg>
                </template>
                {{ retryingAll === 'failed' ? 'Retrying…' : 'Retry all retryable' }}
              </AppButton>
              <AppButton
                variant="mini"
                size="sm"
                :loading="retryingAll === 'permanently_failed'"
                :disabled="counts.terminal === 0 || retryingAll !== null"
                @click="openConfirm('permanently_failed', counts.terminal)"
              >
                {{ retryingAll === 'permanently_failed' ? 'Resetting…' : 'Reset all terminal' }}
              </AppButton>
            </div>
          </div>

          <div class="searchbar">
            <SearchInput v-model="search" placeholder="Search series…" />
          </div>

          <!-- Surfaced retry failure — visible + dismissible, never a console-only error (§16). -->
          <ErrorBanner v-if="retryError" class="downloads__error" :message="retryError" @dismiss="emit('dismiss-error')" />
        </div>

        <div class="downloads__list">
          <EmptyState v-if="failedRows.length === 0" title="No failed downloads" icon-tone="dl-ok-icon">
            <template #icon>
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M20 6L9 17l-5-5" />
              </svg>
            </template>
          </EmptyState>

          <div v-else class="rows">
            <FailedDownloadCard
              v-for="row in failedRows"
              :key="row.chapterId"
              :item="row"
              :retrying="isRetrying(row.chapterId)"
              :expanded="expandedId === row.chapterId"
              @open-series="emit('open-series', $event)"
              @retry="emit('retry', $event)"
              @toggle-expand="toggleExpand(row.chapterId)"
            />
          </div>
        </div>
      </template>

      <!-- ===================== QUEUED ===================== -->
      <template v-else>
        <div class="downloads__toolbar">
          <div class="queued-head">
            <div class="searchbar">
              <SearchInput v-model="search" placeholder="Search series…" />
            </div>
            <label class="toggle">
              <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
              <Toggle v-model="upgradesOnly" :ariaLabel="'Upgrades only'" />
              <span class="toggle__label">Upgrades only</span>
            </label>
          </div>
        </div>

        <div class="downloads__list">
          <EmptyState
            v-if="queuedRows.length === 0"
            title="No chapters queued"
            sub="Library is up to date."
            icon-tone="faint"
          >
            <template #icon>
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="9" />
                <path d="M12 7v5l3 2" />
              </svg>
            </template>
          </EmptyState>

          <div v-else class="rows">
            <ChapterDownloadRow
              v-for="row in queuedRows"
              :key="row.chapterId"
              :item="row"
              @open-series="emit('open-series', $event)"
            >
              <template #before-badge>
                <span v-if="row.state === 'upgrade_available'" class="upgrade-tag">
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M12 19V5M5 12l7-7 7 7" />
                  </svg>
                  UPGRADE
                </span>
              </template>
            </ChapterDownloadRow>
          </div>
        </div>
      </template>

      <!-- Load more — pinned below the scrolling list (never buried at the bottom of a
           long list) — shown when the active tab has more pages (client search works
           over the loaded page subset; server-side q= is a deliberate future add). -->
      <div v-if="!loading && hasMore" class="downloads__more">
        <AppButton variant="mini" size="sm" :loading="loadingMore" @click="emit('load-more')">
          Load more · {{ items.length }} of {{ total }}
        </AppButton>
      </div>
    </div>

    <!-- Requeue-confirm modal -->
    <RequeueConfirmModal
      :open="confirm !== null"
      :count="confirm?.count ?? 0"
      @confirm="confirmRequeue"
      @cancel="confirm = null"
      @update:open="(v) => { if (!v) confirm = null }"
    />
  </div>
</template>

<style scoped>
/* QCAT-231 "fit the screen, scroll inside": `.downloads` is a viewport-fitting
 * flex column, NOT a naturally page-scrolling container. `.downloads__top`
 * (tabs/cycle/run-now + the §16 result line) is short, fixed-content, and
 * flows at its natural height; `.downloads__body` below takes the rest of the
 * viewport and is itself bounded, so its own `.downloads__list` can inner-
 * scroll (see that rule) instead of growing the whole page. `min-height: 0` on
 * the flex column lets it actually shrink below content size — the same
 * grid/flex overflow trap documented on SeriesDetail's `.columns` /
 * PanelCard's `.panel`. Holds at every width (QCAT-230) — Downloads never
 * splits into side-by-side columns, so no mobile override is needed here
 * (contrast SeriesDetail's `.columns`, which stacks two panels).
 */
.downloads {
  display: flex;
  flex-direction: column;
  height: calc(100dvh - 64px);
  min-height: 0;
  padding: 24px 30px 20px;
  background: var(--bg);
}

.downloads__top {
  flex: none;
}

/* Bounded region under `.downloads__top`: a flex column so each tab's fixed
 * `.downloads__toolbar` (when present) stays put and only `.downloads__list`
 * scrolls. `min-height: 0` re-applies the same shrink-to-fit override one
 * level down (a flex ITEM's automatic minimum height is its content size). */
.downloads__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* ---- Tab bar + cycle banner ----------------------------------------------- */
.downloads__head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 18px;
}

/* The cycle pill (+ the "Download now" button that follows it) sits at the far
   right of the head row. */
.downloads__cycle {
  margin-left: auto;
}

/* ---- "Download now" result (§16) ------------------------------------------ */
.run-note {
  margin: -6px 0 14px;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--dl-ok-icon);
}

.run-error {
  margin: -6px 0 14px;
}

/* ---- Per-tab toolbar (sub-tabs/search/toggle) — fixed above the scrolling list */
.downloads__toolbar {
  flex: none;
}

/* ---- The bounded, independently-scrolling row list (QCAT-231) ------------- */
/* `flex: 1` takes whatever height `.downloads__body` has left after its
 * sibling `.downloads__toolbar` (when rendered); `min-height: 0` is the SAME
 * flex-shrink override one level deeper again — without it this region would
 * grow to fit every row instead of scrolling, and the page-level scrollbar
 * comes back (exactly the trap PanelCard's `.panel__content` documents). */
.downloads__list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 4px;
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

/* ---- Surfaced retry-error banner spacing (§16) ---------------------------- */
.downloads__error {
  margin-bottom: 14px;
}

/* ---- Row list ------------------------------------------------------------- */
.rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

/* ---- Progress (active rows) ----------------------------------------------- */
/* The bar itself is the shared <ProgressBar> atom (gradient tone): indeterminate
   until the first download.progress event arrives, then determinate (row.progress).
   This wrapper pins the prototype's 90px thumb width (the atom is full-width by
   default) and stacks the "12 / 40" page counter beneath the bar once page totals
   are known. */
.downloads__progress {
  width: 90px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.downloads__pages {
  font-size: 10.5px;
  font-weight: var(--weight-bold);
  color: var(--faint);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

/* ---- Load more (pagination) ----------------------------------------------- */
.downloads__more {
  display: flex;
  justify-content: center;
  margin-top: 24px;
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

@media (max-width: 900px) {
  /* `.downloads__cycle`'s `margin-left: auto` assumes a single, non-wrapped
     head row; once `.downloads__head` wraps (SegmentedTabs' own tabs already
     wrap independently — see SegmentedTabs.vue), the auto margin instead
     shoves the cycle pill to the far right of whichever line it lands on,
     leaving a large, odd gap. Let it sit naturally after the tabs/wherever it
     wraps to instead. */
  .downloads__cycle {
    margin-left: 0;
  }

  /* `.subhead__actions`'s "Retry all retryable" + "Reset all terminal" pair
     can't fit one line beside the failed sub-tabs on a phone; once `.subhead`
     wraps them onto their own line, drop the `margin-left: auto` (nothing to
     align against any more) and let the two buttons wrap onto a further line
     themselves rather than overflow the viewport width. */
  .subhead__actions {
    flex: 1 1 100%;
    flex-wrap: wrap;
    margin-left: 0;
  }
}
</style>
