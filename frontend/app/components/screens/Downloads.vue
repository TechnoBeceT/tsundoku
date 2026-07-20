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
import CycleTimers from '../downloads/CycleTimers.vue'
import SourceStatusStrip from '../downloads/SourceStatusStrip.vue'
import type { SourceStatus } from '../downloads/sourceStatus.types'
import DeferralNote from '../downloads/DeferralNote.vue'
import FailedDownloadCard from '../downloads/FailedDownloadCard.vue'
import ActiveFailureBanner from '../downloads/ActiveFailureBanner.vue'
import RequeueConfirmModal from '../downloads/RequeueConfirmModal.vue'
import { useNow } from '../../composables/useNow'
import type { DownloadItem, DownloadState, DownloadTab, RetryAllState } from './downloads.types'
import { isFailureRow, isRetryableFailure, isTerminalFailure, failSubTabPredicate } from './downloads.failures'

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
   * Server badge counts. `allFailures` is the HONEST failed-set total (state-failed
   * ∪ source-failing) — so the Failed badge counts broken UPGRADES too. The
   * retryable/terminal sub-tab split is derived over the loaded failed page (the API
   * offers no per-flag count). Defaulted to zeros so stories that omit it still render.
   */
  counts?: { active: number; queued: number; allFailures: number }
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
  /**
   * Sources whose circuit-breaker is tripped (anti-ban cooldown) right now, from the
   * live SSE `sources.summary` signal. Drives the Active-tab "M sources cooling down"
   * awareness banner — so an empty Active list reads as WAITING, not "up to date".
   */
  coolingDownSources?: number
  /** Whether a download cycle is running now (SSE) — drives the CycleTimers header. */
  downloadRunning?: boolean
  /** Whether a refresh sweep is running now (SSE) — drives the CycleTimers header. */
  refreshRunning?: boolean
  /** Milliseconds until the next download cycle; null → "waiting…". */
  downloadRemainingMs?: number | null
  /** Milliseconds until the next refresh sweep; null → "waiting…". */
  refreshRemainingMs?: number | null
  /** The live per-source status strip (sources downloading / cooling right now). */
  sourceStatuses?: SourceStatus[]
}>(), {
  activeTab: 'active',
  cycleActive: false,
  nextCycleMinutes: null,
  retryingIds: () => [],
  retryingAll: null,
  retryError: '',
  loading: false,
  counts: () => ({ active: 0, queued: 0, allFailures: 0 }),
  total: 0,
  hasMore: false,
  loadingMore: false,
  running: false,
  runMessage: '',
  runError: '',
  coolingDownSources: 0,
  downloadRunning: false,
  refreshRunning: false,
  downloadRemainingMs: null,
  refreshRemainingMs: null,
  sourceStatuses: () => [],
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
  /** Open the source-health view (from the Active-tab cooling-down banner). */
  'open-health': []
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

// The loaded failed set — every failure row on the current Failed page (state-failed
// ∪ source-failing). The retryable/terminal sub-tab counts are DERIVED from it (the
// documented loaded-page caveat) since the API offers no per-flag server count; the
// "All failures" badge stays the exact server total (counts.allFailures).
const loadedFailures = computed(() => props.items.filter(isFailureRow))
const retryableCount = computed(() => loadedFailures.value.filter(isRetryableFailure).length)
const terminalCount = computed(() => loadedFailures.value.filter(isTerminalFailure).length)

const mainTabs = computed(() => [
  { key: 'active', label: 'Active', count: counts.value.active },
  { key: 'failed', label: 'Failed', count: counts.value.allFailures },
  { key: 'queued', label: 'Queued', count: counts.value.queued },
])

const failTabs = computed(() => [
  { key: 'all', label: 'All failures', count: counts.value.allFailures },
  { key: 'retryable', label: 'Retryable', count: retryableCount.value },
  { key: 'terminal', label: 'Terminal', count: terminalCount.value },
])

// ---- Per-tab rows -----------------------------------------------------------
const activeRows = computed(() => applySearch(byState(['downloading', 'upgrading'])))

// The Failed tab routes by the FAILING SOURCE's budget (retryable/terminal), not by
// chapter state — so a downloaded broken-upgrade row lands in Retryable instead of
// vanishing. This is the fix for the empty-Retryable bug.
const failedRows = computed(() =>
  applySearch(loadedFailures.value.filter(failSubTabPredicate(failSubTab.value))),
)

const queuedRows = computed(() => {
  let list = byState(['wanted', 'upgrade_available'])
  if (upgradesOnly.value) list = list.filter((i) => i.state === 'upgrade_available')
  return applySearch(list)
})

// ---- Honest cycle pill: deferral summary ------------------------------------
// A shared ticking clock so the summary drops elapsed cooldowns and the pill's ETA
// stays live (the CycleBanner formats it against the same clock).
const { now } = useNow()

// The queued chapters whose waited-on source is still under a FUTURE cooldown. Read
// from the loaded page only (the documented read-model caveat) — honest, never
// fabricated: when a queued page is loaded and most of it is deferred, the pill can
// say so instead of the misleading "Idle — waiting for next cycle".
const deferralSummary = computed(() => {
  const queued = props.items.filter((i) => i.state === 'wanted' || i.state === 'upgrade_available')
  const deferred = queued.filter((i) => i.deferredUntil != null && new Date(i.deferredUntil).getTime() > now.value)
  // Only speak up when ALL/MOST of the loaded queue is waiting — a few deferred rows
  // among many ready ones means the cycle IS making progress, so keep the idle text.
  if (deferred.length === 0 || deferred.length * 2 < queued.length) return null
  const soonest = Math.min(...deferred.map((i) => new Date(i.deferredUntil!).getTime()))
  return { count: deferred.length, soonestIso: new Date(soonest).toISOString() }
})

// ---- Active-tab failure awareness -------------------------------------------
// When the Active list is empty, "up to date" is a LIE if chapters are failing or
// sources are cooling down. Total failing = the honest failed-set total (state-failed
// ∪ source-failing) — so a wave of broken upgrades still lights the banner.
const failingCount = computed(() => counts.value.allFailures)
const showActiveBanner = computed(
  () => activeRows.value.length === 0 && (failingCount.value > 0 || props.coolingDownSources > 0),
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
    <!-- The flowing top: tabs/cycle/run-now + the §16 result line. Everything on
         this screen now grows in the document (QCAT-265) — no letterbox, no
         inner-scroll; the page itself scrolls. -->
    <div class="downloads__top">
      <!-- Top-level tabs + cycle banner + manual "Download now" trigger -->
      <div class="downloads__head">
        <SegmentedTabs :model-value="activeTab" :tabs="mainTabs" @update:model-value="selectTab($event as DownloadTab)" />
        <CycleBanner class="downloads__cycle" :cycle-active="cycleActive" :next-cycle-minutes="nextCycleMinutes" :deferral-summary="deferralSummary" />
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

      <!-- Engine visibility (near the header): the two live cycle countdowns and
           the per-source status strip (which sources are downloading / cooling
           right now). Both are additive to the existing CycleBanner. -->
      <div class="downloads__status">
        <CycleTimers
          :download-running="downloadRunning"
          :refresh-running="refreshRunning"
          :download-remaining-ms="downloadRemainingMs"
          :refresh-remaining-ms="refreshRemainingMs"
        />
        <SourceStatusStrip :sources="sourceStatuses" />
      </div>

      <!-- §16 "Download now" result: success note or failure, never swallowed. -->
      <p v-if="runMessage" class="run-note">{{ runMessage }}</p>
      <FormError v-if="runError" class="run-error" :message="runError" />
    </div>

    <!-- The body: each tab's toolbar (sub-tabs/search/toggle) + its row list, all
         flowing in the document. A long Failed/Queued list grows the page (the
         toolbar scrolls away with it) rather than scrolling inside a bounded box. -->
    <div class="downloads__body">
      <!-- Loading skeletons -->
      <div v-if="loading" class="downloads__list">
        <div class="rows">
          <Skeleton v-for="n in skeletons" :key="n" variant="row" height="4.75rem" />
        </div>
      </div>

      <!-- ===================== ACTIVE ===================== -->
      <div v-else-if="activeTab === 'active'" class="downloads__list">
        <!-- Failure awareness: the Active list can be empty while chapters FAIL or
             sources cool down — say so instead of a misleading "up to date". -->
        <ActiveFailureBanner
          v-if="showActiveBanner"
          :failing="failingCount"
          :cooling-down="coolingDownSources"
          @view-failed="selectTab('failed')"
          @view-sources="emit('open-health')"
        />

        <EmptyState
          v-if="activeRows.length === 0"
          title="No active downloads"
          :sub="showActiveBanner ? 'Nothing is fetching right now — see above.' : 'Waiting for the next download cycle.'"
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
                :disabled="retryableCount === 0 || retryingAll !== null"
                @click="openConfirm('failed', retryableCount)"
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
                :disabled="terminalCount === 0 || retryingAll !== null"
                @click="openConfirm('permanently_failed', terminalCount)"
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
                <!-- The source is deferred (persisted cooldown) → say WHY it's stuck,
                     in place of the bare UPGRADE tag / "Wanted" badge. The waited-on
                     name is the one already on the row: the upgrade target, else the
                     primary source. -->
                <DeferralNote
                  v-if="row.deferredUntil"
                  :deferred-until="row.deferredUntil"
                  :source="row.upgradeTarget || row.providerName"
                  :reason="row.deferReason"
                  :reason-kind="row.waitingReason"
                />
                <span v-else-if="row.state === 'upgrade_available'" class="upgrade-tag">
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
/* The old QCAT-231 letterbox (`height: calc(100dvh - 64px)` + a flex-fill chain
 * bounding `.downloads__body`/`.downloads__list` into an inner-scroll region)
 * was experience drift (§0.1): on a large screen the owner was working inside a
 * small letterboxed area. Stripped — no viewport-keyed height, no inner-scroll:
 * the toolbar + row list flow in the document and the PAGE scrolls (QCAT-265,
 * the GROW case for a single-column activity list). Spacing is on the fluid
 * token ladder (byte-identical at the 16px desktop anchor: 24px 30px sides, 20px
 * trailing). `--app-nav-bottom` (0 on desktop) clears the phone bottom-nav so
 * the last row is never occluded. */
.downloads {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-2xl-tight) + var(--app-nav-bottom));
  background: var(--bg);
}

/* ---- Tab bar + cycle banner ----------------------------------------------- */
.downloads__head {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  flex-wrap: wrap;
  margin-bottom: var(--space-xl);
}

/* The cycle pill (+ the "Download now" button that follows it) sits at the far
   right of the head row. */
.downloads__cycle {
  margin-left: auto;
}

/* ---- Engine-visibility status row (timers + source strip) ----------------- */
.downloads__status {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  flex-wrap: wrap;
  margin-bottom: var(--space-lg);
}

/* ---- "Download now" result (§16) ------------------------------------------ */
.run-note {
  margin: calc(var(--space-xs-tight) * -1) 0 var(--space-base);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--dl-ok-icon);
}

.run-error {
  margin: calc(var(--space-xs-tight) * -1) 0 var(--space-base);
}

/* ---- The row list (grows with content; the page scrolls, QCAT-265) -------- */
.downloads__list {
  padding-bottom: var(--space-2xs);
}

/* ---- Failed sub-head + bulk actions --------------------------------------- */
.subhead {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  flex-wrap: wrap;
  margin-bottom: var(--space-lg);
}

.subhead__actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: var(--space-xs);
}

/* ---- Queued head ---------------------------------------------------------- */
.queued-head {
  display: flex;
  align-items: center;
  gap: var(--space-base);
  flex-wrap: wrap;
  margin-bottom: var(--space-base);
}

/* ---- Search --------------------------------------------------------------- */
.searchbar {
  width: 18.75rem; /* 300px @16 — byte-identical rem literal */
  max-width: 100%;
  margin-bottom: var(--space-base);
}

.queued-head .searchbar {
  margin-bottom: 0;
}

/* ---- Upgrades-only toggle ------------------------------------------------- */
.toggle {
  display: flex;
  align-items: center;
  gap: 0.5625rem; /* 9px @16 — off-ladder, byte-identical rem literal */
}

.toggle__label {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

/* ---- Surfaced retry-error banner spacing (§16) ---------------------------- */
.downloads__error {
  margin-bottom: var(--space-base);
}

/* ---- Row list ------------------------------------------------------------- */
.rows {
  display: flex;
  flex-direction: column;
  gap: var(--space-sm);
}

/* ---- Progress (active rows) ----------------------------------------------- */
/* The bar itself is the shared <ProgressBar> atom (gradient tone): indeterminate
   until the first download.progress event arrives, then determinate (row.progress).
   This wrapper pins the prototype's 90px thumb width (the atom is full-width by
   default) and stacks the "12 / 40" page counter beneath the bar once page totals
   are known. */
.downloads__progress {
  width: 5.625rem; /* 90px @16 — byte-identical rem literal */
  flex: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2xs);
}

.downloads__pages {
  font-size: 0.65625rem; /* 10.5px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-bold);
  color: var(--faint);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

/* ---- Load more (pagination) ----------------------------------------------- */
.downloads__more {
  display: flex;
  justify-content: center;
  margin-top: var(--space-2xl);
}

/* ---- Upgrade tag (queued) ------------------------------------------------- */
.upgrade-tag {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2xs);
  font-size: 0.65625rem; /* 10.5px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-extrabold);
  padding: var(--space-3xs) var(--space-xs);
  border-radius: var(--radius-pill);
  background: var(--dl-queued-bg);
  color: var(--dl-queued-text);
}

@media (max-width: 900px) {
  /* QCAT-261 mobile-compact: halve the side gutters so a phone packs content
     densely (Komikku), and clear the fixed phone bottom-nav. Desktop unchanged. */
  .downloads {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }

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
