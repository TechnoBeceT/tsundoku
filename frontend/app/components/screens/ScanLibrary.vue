<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Stepper from '../ui/Stepper.vue'
import MatchPanel from '../scanLibrary/MatchPanel.vue'
import ScanProgress from '../scanLibrary/ScanProgress.vue'
import StagingTable from '../scanLibrary/StagingTable.vue'
import type { StepItem } from '../ui/nav.types'
import type { SearchGroup } from './import.types'
import type { BatchImportResult, ScanEntry, ScanMatch, ScanState, ScanStatusFilter } from './scanLibrary.types'

/**
 * ScanLibrary — the Scan Library wizard (migrating an existing on-disk manga
 * library into Tsundoku without re-downloading). A THIN container: it owns
 * only the two-stage `<Stepper>` presentation (Scan → Review), composes
 * `<ScanProgress>` + `<StagingTable>`, and forwards every action as an emit —
 * no fetching, routing, or stores. Data (scan state, staged entries, mutation
 * busy/error) arrives entirely via props from `pages/scan-library.vue`, which
 * owns `useScanLibrary()`.
 *
 * Stage rule: **Scan** shows only while nothing has happened yet (idle status
 * AND zero staged entries — a first-ever visit). The moment a scan starts OR
 * any staged entry already exists (the owner reopened the page after a prior
 * scan), the screen jumps to **Review** — its header carries the live
 * `<ScanProgress>` bar + a "Scan again" button, so the owner can kick off
 * another pass without losing sight of the table underneath.
 *
 * Within Review, a row's `match` emit opens the `<MatchPanel>` sub-panel
 * (Task 7) IN PLACE of the header + `<StagingTable>` — `matchPath` (set by
 * the page once `useScanLibrary().match(path)` resolves) gates which one
 * renders. The panel's own `confirm`/`back` re-emit as `match-confirm` /
 * `match-back` (carrying the target `path` alongside the picked source, since
 * `<MatchPanel>` itself only knows the selection) for the page to call
 * `importWithMatch` and close the panel.
 *
 * §16 no-silent-failure: a `scanState.error` (a failed/timed-out scan) is
 * rendered via `<ErrorBanner>` regardless of stage — `scan.done` is terminal
 * (the composable already latches it and ignores late progress frames), so
 * this screen just renders whatever `status`/`error` it's handed.
 */
const props = withDefaults(defineProps<{
  /** The live scan lifecycle (idle / scanning / done), incl. any error. */
  scanState: ScanState
  /** The current page of staged entries. */
  entries: ScanEntry[]
  /** The active staging-status filter. */
  statusFilter?: ScanStatusFilter
  /** True while the entries list is loading (first page or load-more). */
  pending?: boolean
  /** A load failure for the entries list itself, or "" for none. */
  entriesError?: string
  /** Whether a full page came back (more entries may exist). */
  hasMore?: boolean
  /** Paths whose skip/import mutation is currently in flight. */
  busyPaths?: string[]
  /** Path → last mutation error, for rows with a surfaced failure. */
  rowErrors?: Record<string, string>
  /** True while the bulk "import all remaining" batch is running. */
  batchImporting?: boolean
  /** A load/network failure for the bulk batch itself, or "" for none. */
  batchError?: string
  /** The last bulk batch's outcome, or null before any run / after dismissal. */
  batchResult?: BatchImportResult | null
  /** The staged entry currently in the Match sub-panel, or null when the table shows instead. */
  matchPath?: string | null
  /** The match target's title, for the sub-panel's header. */
  matchTitle?: string
  /** Cross-source candidate groups for the current match target. */
  matchGroups?: SearchGroup[]
  /** True while the match search itself (not the confirm mutation) is in flight. */
  matching?: boolean
  /** A match-search failure message, or "" for none. */
  matchError?: string
}>(), {
  statusFilter: null,
  pending: false,
  entriesError: '',
  hasMore: false,
  busyPaths: () => [],
  rowErrors: () => ({}),
  batchImporting: false,
  batchError: '',
  batchResult: null,
  matchPath: null,
  matchTitle: '',
  matchGroups: () => [],
  matching: false,
  matchError: '',
})

const emit = defineEmits<{
  /** Launch (or re-launch) the background disk scan. */
  'start-scan': []
  /** The status filter tab changed. */
  'set-status-filter': [status: ScanStatusFilter]
  /** Load the next page of the current filter. */
  'load-more': []
  /** Import one staged entry disk-only. */
  'import-disk-only': [path: string]
  /** Open the cross-source match search for one staged entry. */
  'match': [path: string]
  /** Mark one staged entry skipped. */
  'skip': [path: string]
  /** Import every remaining pending entry disk-only. */
  'import-all-disk-only': []
  /** The owner confirmed a source for the current match target. */
  'match-confirm': [payload: { path: string, match: ScanMatch }]
  /** Abandon the match sub-panel and return to the staging table. */
  'match-back': []
}>()

const stepItems: StepItem[] = [
  { key: 'scan', label: 'Scan' },
  { key: 'review', label: 'Review' },
]

// Nothing has happened yet: fresh idle state with no staged entries at all —
// show the Scan launcher. Anything else (a scan is running/done, or entries
// already exist from a prior visit) is the Review stage.
const currentStep = computed(() => (props.scanState.status === 'idle' && props.entries.length === 0) ? 'scan' : 'review')

const scanning = computed(() => props.scanState.status === 'scanning')

// Pending count (across the loaded page) gates the bulk import-all button —
// there's nothing to drain if nothing pending is currently in view. Gated
// strictly on entries actually present with status 'pending': the tab
// filter alone says nothing about the CURRENT page's contents, so a stale
// "All"/"Pending" tab selection with zero pending rows in view must NOT
// enable the button (the bug this guards against: a second "Import all
// remaining" click after everything is already imported/skipped silently
// no-opped because the button stayed enabled on those tabs).
const hasPending = computed(() => props.entries.some((e) => e.status === 'pending'))

// The Match sub-panel replaces the staging table entirely while a target is set.
const showMatchPanel = computed(() => props.matchPath != null)

// Busy/error for the match target's CONFIRM mutation reuse the SAME per-path
// lookups the table already receives (§2 DRY) — no separate prop needed.
const matchBusy = computed(() => props.matchPath != null && props.busyPaths.includes(props.matchPath))
const matchRowError = computed(() => (props.matchPath != null ? (props.rowErrors[props.matchPath] ?? '') : ''))
</script>

<template>
  <div class="scan-library">
    <div class="scan-library__shell">
      <div class="scan-library__steps">
        <Stepper :steps="stepItems" :current="currentStep" orientation="horizontal" />
      </div>

      <div class="scan-library__panel">
        <!-- §16: a failed/timed-out scan is ALWAYS visible, whichever stage renders. -->
        <ErrorBanner v-if="scanState.error" class="scan-library__scan-error" :message="scanState.error" :dismissible="false" />

        <!-- ================= Stage: Scan ================= -->
        <section v-if="currentStep === 'scan'" class="sl-stage sl-stage--center">
          <p class="sl-intro">
            Scan your existing on-disk library to bring it into Tsundoku without re-downloading anything.
          </p>
          <AppButton variant="primary" size="lg" @click="emit('start-scan')">
            Start scan
          </AppButton>
        </section>

        <!-- ================= Stage: Review ================= -->
        <section v-else class="sl-stage">
          <!-- The Match sub-panel takes over the whole Review body while a target is set. -->
          <MatchPanel
            v-if="showMatchPanel"
            :path="matchPath!"
            :title="matchTitle"
            :groups="matchGroups"
            :searching="matching"
            :search-error="matchError"
            :busy="matchBusy"
            :error="matchRowError"
            @confirm="emit('match-confirm', { path: matchPath!, match: $event })"
            @back="emit('match-back')"
          />

          <template v-else>
            <div class="sl-review-head">
              <ScanProgress v-if="scanning" class="sl-review-head__progress" :processed="scanState.processed" :total="scanState.total" />
              <span v-else class="sl-review-head__done">
                {{ scanState.error ? 'Scan incomplete' : (scanState.status === 'done' ? `Scan complete · ${scanState.total} found` : 'Ready to scan') }}
              </span>
              <div class="sl-review-head__actions">
                <AppButton variant="ghost" size="sm" :disabled="scanning" :loading="scanning" @click="emit('start-scan')">
                  Scan again
                </AppButton>
                <AppButton
                  variant="mini"
                  size="sm"
                  :loading="batchImporting"
                  :disabled="batchImporting || !hasPending"
                  @click="emit('import-all-disk-only')"
                >
                  Import all remaining · disk-only
                </AppButton>
              </div>
            </div>

            <ErrorBanner v-if="batchError" class="sl-review-head__error" :message="batchError" :dismissible="false" />
            <p v-else-if="batchResult" class="sl-batch-result" :class="{ 'sl-batch-result--warn': batchResult.failed.length > 0 }">
              Imported {{ batchResult.imported }} disk-only<template v-if="batchResult.failed.length">
                · {{ batchResult.failed.length }} failed
              </template>.
            </p>

            <StagingTable
              :entries="entries"
              :status-filter="statusFilter"
              :pending="pending"
              :entries-error="entriesError"
              :has-more="hasMore"
              :busy-paths="busyPaths"
              :row-errors="rowErrors"
              @set-status-filter="emit('set-status-filter', $event)"
              @load-more="emit('load-more')"
              @import-disk-only="emit('import-disk-only', $event)"
              @match="emit('match', $event)"
              @skip="emit('skip', $event)"
            />
          </template>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.scan-library {
  padding: 28px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

.scan-library__shell {
  max-width: 880px;
  margin: 0 auto;
}

.scan-library__steps {
  margin-bottom: 24px;
}

.scan-library__panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 22px;
}

.scan-library__scan-error {
  margin-bottom: 18px;
}

.sl-stage--center {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 22px;
  padding: 48px 20px;
  text-align: center;
}

.sl-intro {
  margin: 0;
  max-width: 440px;
  font-size: var(--text-base);
  color: var(--muted);
  line-height: 1.5;
}

.sl-review-head {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}

.sl-review-head__progress {
  flex: 1;
  min-width: 220px;
}

.sl-review-head__done {
  flex: 1;
  min-width: 160px;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.sl-review-head__actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
}

.sl-review-head__error {
  margin-bottom: 14px;
}

.sl-batch-result {
  margin: 0 0 14px;
  padding: 10px 13px;
  border-radius: var(--radius-md);
  background: var(--surface2);
  border: 1px solid var(--border);
  color: var(--muted);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
}

.sl-batch-result--warn {
  background: var(--danger-bg);
  border-color: var(--danger-border);
  color: var(--danger-text);
}
</style>
