<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import DisclosurePanel from '../ui/DisclosurePanel.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Stepper from '../ui/Stepper.vue'
import MatchPanel from '../scanLibrary/MatchPanel.vue'
import ScanProgress from '../scanLibrary/ScanProgress.vue'
import StagingTable from '../scanLibrary/StagingTable.vue'
import SourceFilterChips from '../ui/SourceFilterChips.vue'
import type { ProviderRef } from '~/composables/useSourceConfigure'
import type { StepItem } from '../ui/nav.types'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup, Source } from './import.types'
import type { BatchImportResult, ScanEntry, ScanState, ScanStatusFilter } from './scanLibrary.types'

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
 * (Task 7, rebuilt multi-select in Slice P) IN PLACE of the header +
 * `<StagingTable>` — `matchPath` (set by the page once
 * `useScanLibrary().match(path)` resolves) gates which one renders. The
 * panel's own `confirm`/`loadBreakdowns`/`back` re-emit as `match-confirm` /
 * `load-breakdowns` / `match-back` (carrying the target `path` alongside the
 * gathered, ranked `ProviderRef[]`, since `<MatchPanel>` itself only knows the
 * selection) for the page to call `importWithMatches` / `loadBreakdowns` and
 * close the panel.
 *
 * A page-level "Limit matches to:" `<SourceFilterChips>` row sits above the
 * staging table (only when a `sources` list is supplied): the owner picks a
 * subset once and every entry's cross-source match respects it. The screen
 * stays pure-presentational — it holds no source state, just v-models
 * `sourceFilter` up to the page via `update:sourceFilter`.
 *
 * §16 no-silent-failure: a `scanState.error` (a failed/timed-out scan) is
 * rendered via `<ErrorBanner>` regardless of stage — `scan.done` is terminal
 * (the composable already latches it and ignores late progress frames), so
 * this screen just renders whatever `status`/`error` it's handed.
 */
const props = withDefaults(defineProps<{
  /** The live scan lifecycle (idle / scanning / done), incl. any error. */
  scanState: ScanState
  /** Sources available to restrict matches to (populates the filter chips); empty hides the row. */
  sources?: Source[]
  /** The currently-selected match-filter source IDs (v-model:sourceFilter). */
  sourceFilter?: string[]
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
  /** Per-scanlator breakdown cache, keyed `source:mangaId` (see `useSourceConfigure`). */
  matchBreakdowns?: Record<string, ScanlatorCoverage[] | null>
  /** True while the match search itself (not the confirm mutation) is in flight. */
  matching?: boolean
  /** A match-search failure message, or "" for none. */
  matchError?: string
}>(), {
  sources: () => [],
  sourceFilter: () => [],
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
  matchBreakdowns: () => ({}),
  matching: false,
  matchError: '',
})

const emit = defineEmits<{
  /** The match source-filter selection changed (v-model:sourceFilter). */
  'update:sourceFilter': [ids: string[]]
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
  /** The owner confirmed the gathered, ranked sources for the current match target. */
  'match-confirm': [payload: { path: string, matches: ProviderRef[] }]
  /** Fetch the per-scanlator breakdown for every given candidate (Configure-stage entry). */
  'load-breakdowns': [candidates: SearchCandidate[]]
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
        <!-- QCAT-265 GROW: the Review body and the Match sub-panel each flow in
             the document at their natural height — the PAGE scrolls, nothing is
             letterboxed to the viewport (a 1000-series scan grows the table and
             page-scrolls; a long cross-source match list does the same). The old
             QCAT-231 per-stage inner-scroll was experience drift (§0.1) — see the
             style block below. -->
        <section v-else class="sl-stage">
          <!-- The Match sub-panel takes over the whole Review body while a target is set. -->
          <MatchPanel
            v-if="showMatchPanel"
            :title="matchTitle"
            :groups="matchGroups"
            :breakdowns="matchBreakdowns"
            :searching="matching"
            :search-error="matchError"
            :busy="matchBusy"
            :error="matchRowError"
            @confirm="emit('match-confirm', { path: matchPath!, matches: $event })"
            @load-breakdowns="emit('load-breakdowns', $event)"
            @back="emit('match-back')"
          />

          <div v-else class="sl-review-body">
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

            <!-- Page-level source filter (QCAT-265 treatment #2 — DISCLOSURE):
                 the source list is 40+ entries in prod (a long list IN THE WAY
                 of the review flow), so it collapses to a tap-to-open panel
                 instead of an always-on ~20-row chip cloud — the owner's
                 "exclude sources … open/close that list is more smart". Chosen
                 once, every entry's cross-source match respects it (only when a
                 source list was supplied). Flat (no second card outline inside);
                 `bounded="false"` so the chips grow when open and the page
                 scrolls (no nested scroll band). Mirrors Import.vue. -->
            <DisclosurePanel
              v-if="sources.length"
              class="sl-review-filter"
              collapsible
              flat
              :default-open="false"
              title="Limit matches to sources"
              :summary="sourceFilter.length ? `${sourceFilter.length} selected` : ''"
            >
              <SourceFilterChips
                :sources="sources"
                :selected="sourceFilter"
                :bounded="false"
                label=""
                @update:selected="emit('update:sourceFilter', $event)"
              />
            </DisclosurePanel>

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
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* The old QCAT-231 letterbox (`height: calc(100dvh - 64px)` + a flex-fill chain
 * bounding `.sl-review-list` / `.sl-match-panel` into inner-scroll regions) was
 * experience drift (§0.1): on a large screen the owner was working inside a small
 * letterboxed area with a tiny scrollable table sunk in an empty card. Stripped —
 * no viewport-keyed height, no per-stage inner-scroll: the wizard GROWS with its
 * content and the PAGE scrolls (QCAT-265, the GROW case for a single-column
 * wizard). Spacing is on the fluid token ladder (byte-identical at the 16px
 * anchor: 24px 30px sides, 20px trailing). `--app-nav-bottom` (0 on desktop)
 * clears the phone bottom-nav so the last action/row is never occluded. */
.scan-library {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-2xl-tight) + var(--app-nav-bottom));
  background: var(--bg);
}

/* The centred reading column. `max-width` on rem so the measure stays constant
 * as the fluid root scales the type inside it. */
.scan-library__shell {
  max-width: 55rem; /* 880px @16 */
  width: 100%;
  margin: 0 auto;
}

.scan-library__steps {
  margin-bottom: var(--space-2xl); /* 24px @16 */
}

/* The bordered panel the stages flow inside; grows with its content (QCAT-265). */
.scan-library__panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 1.375rem; /* 22px @16 — off-ladder, byte-identical rem literal */
}

.scan-library__scan-error {
  margin-bottom: var(--space-xl); /* 18px @16 */
}

/* Both `.sl-stage` variants flow in the document (QCAT-265 GROW). The Scan
 * launcher (`--center`) centres its intro + button; the Review variant stacks
 * head/errors/filter/table, all at natural height — the page scrolls. */
.sl-stage {
  display: flex;
  flex-direction: column;
}

.sl-stage--center {
  align-items: center;
  gap: 1.375rem; /* 22px @16 — off-ladder, byte-identical rem literal */
  padding: 3rem var(--space-2xl-tight); /* 48px 20px @16 */
  text-align: center;
}

.sl-intro {
  margin: 0;
  max-width: 27.5rem; /* 440px @16 */
  font-size: var(--text-base);
  color: var(--muted);
  line-height: 1.5;
}

/* The Review body: everything except the Match sub-panel. A flex column that
 * grows with its content (head/errors/filter chips/table all flow, page
 * scrolls — QCAT-265 GROW). */
.sl-review-body {
  display: flex;
  flex-direction: column;
}

.sl-review-head {
  display: flex;
  align-items: center;
  gap: var(--space-lg); /* 16px @16 */
  flex-wrap: wrap;
  margin-bottom: var(--space-lg); /* 16px @16 */
}

.sl-review-head__progress {
  flex: 1;
  min-width: 13.75rem; /* 220px @16 */
}

.sl-review-head__done {
  flex: 1;
  min-width: 10rem; /* 160px @16 */
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.sl-review-head__actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--space-xs); /* 8px @16 */
}

.sl-review-head__error {
  margin-bottom: var(--space-base); /* 14px @16 */
}

.sl-batch-result {
  margin: 0 0 var(--space-base); /* 14px @16 */
  padding: var(--space-sm) 0.8125rem; /* 10px 13px @16 — 13px off-ladder literal */
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

.sl-review-filter {
  margin-bottom: var(--space-base); /* 14px @16 */
}

@media (max-width: 900px) {
  /* "Scan again" + "Import all remaining · disk-only" can't share one line
   * once `.sl-review-head` has already wrapped them below the progress/done
   * label — let the two buttons wrap onto their own further lines too rather
   * than overflow a phone's width (the long "Import all remaining ·
   * disk-only" label is the tight one). */
  .sl-review-head__actions {
    flex-wrap: wrap;
  }
}
</style>
