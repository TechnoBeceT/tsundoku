<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import Skeleton from '../ui/Skeleton.vue'
import StagingRow from './StagingRow.vue'
import type { TabItem } from '../ui/nav.types'
import type { ScanEntry, ScanStatusFilter } from '../screens/scanLibrary.types'

/**
 * StagingTable — the Scan Library review list: a status-filter tab bar (All /
 * Pending / Imported / Skipped — "All" reflects the composable's own default,
 * `statusFilter === null`, so the tab bar never shows nothing selected), the
 * paginated `StagingRow` list itself, and a "Load more" button when a full
 * page came back (mirrors the Downloads screen's pagination affordance —
 * `GET /api/library/imports` carries no total count, so "more may exist" is
 * the only signal available).
 *
 * Presentational only: `entries` arrive already filtered/paginated by the
 * parent; every row action bubbles straight up. `busyPaths`/`rowErrors` are
 * plain per-path lookups (not functions — matches the `retryingIds`
 * convention in `Downloads.vue`) so this stays a pure props+emits component.
 */
const props = withDefaults(defineProps<{
  /** The current page of staged entries (already filtered by `statusFilter`). */
  entries: ScanEntry[]
  /** The active staging-status filter (`null` = All). */
  statusFilter?: ScanStatusFilter
  /** True while the entries list (first page or a load-more page) is loading. */
  pending?: boolean
  /** A load failure for the entries list itself, or "" for none. */
  entriesError?: string
  /** Whether a full page came back — a "Load more" button appears when true. */
  hasMore?: boolean
  /** Paths whose skip/import mutation is currently in flight. */
  busyPaths?: string[]
  /** Path → last mutation error, for rows with a surfaced failure. */
  rowErrors?: Record<string, string>
}>(), {
  statusFilter: null,
  pending: false,
  entriesError: '',
  hasMore: false,
  busyPaths: () => [],
  rowErrors: () => ({}),
})

const emit = defineEmits<{
  /** The status filter tab changed. */
  'set-status-filter': [status: ScanStatusFilter]
  /** Load the next page of the current filter and append the results. */
  'load-more': []
  /** Import one entry disk-only. */
  'import-disk-only': [path: string]
  /** Open the cross-source match search for one entry. */
  'match': [path: string]
  /** Mark one entry skipped. */
  'skip': [path: string]
}>()

const TABS: TabItem[] = [
  { key: 'all', label: 'All' },
  { key: 'pending', label: 'Pending' },
  { key: 'imported', label: 'Imported' },
  { key: 'skipped', label: 'Skipped' },
]

const activeTabKey = computed(() => props.statusFilter ?? 'all')

function selectTab(key: string): void {
  emit('set-status-filter', key === 'all' ? null : (key as Exclude<ScanStatusFilter, null>))
}

const isRowBusy = (path: string): boolean => props.busyPaths.includes(path)
const rowError = (path: string): string => props.rowErrors[path] ?? ''

const isInitialLoad = computed(() => props.pending && props.entries.length === 0)
const skeletons = Array.from({ length: 4 }, (_, i) => i)
</script>

<template>
  <div class="staging-table">
    <div class="staging-table__head">
      <SegmentedTabs :model-value="activeTabKey" :tabs="TABS" @update:model-value="selectTab" />
    </div>

    <ErrorBanner v-if="entriesError" class="staging-table__error" :message="entriesError" :dismissible="false" />

    <!-- QCAT-231 "fit the screen, scroll inside": the tab bar above stays
         fixed and ONLY this region scrolls — a 1000-series scan's staging
         table must scroll WITHIN the list, never grow the whole page. -->
    <div class="staging-table__scroll">
      <div v-if="isInitialLoad" class="staging-table__rows">
        <Skeleton v-for="n in skeletons" :key="n" variant="row" height="68px" />
      </div>

      <EmptyState
        v-else-if="entries.length === 0"
        title="No staged entries"
        sub="Nothing matches this filter yet."
        icon-tone="faint"
      />

      <div v-else class="staging-table__rows">
        <StagingRow
          v-for="entry in entries"
          :key="entry.path"
          :entry="entry"
          :busy="isRowBusy(entry.path)"
          :error="rowError(entry.path)"
          @import-disk-only="emit('import-disk-only', $event)"
          @match="emit('match', $event)"
          @skip="emit('skip', $event)"
        />
      </div>
    </div>

    <!-- Pinned BELOW the scroll region (never buried at the bottom of a long
         list, mirrors the Downloads screen's "Load more" affordance). -->
    <div v-if="hasMore" class="staging-table__more">
      <AppButton variant="mini" size="sm" :loading="pending && entries.length > 0" @click="emit('load-more')">
        Load more
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
/* QCAT-231 "fit the screen, scroll inside": a flex column that fills
 * whatever bounded height the parent gives it (`ScanLibrary.vue`'s
 * `.sl-review-list` class, merged onto this component's root — see that
 * file's comment). Outside a bounded ancestor (a bare Storybook story with
 * no fixed-height frame) `flex: 1` on `.staging-table__scroll` simply has
 * nothing to grow into beyond its content, so the story still renders at its
 * natural height — this never breaks an unbounded story (mirrors PanelCard /
 * Downloads' documented fallback). */
.staging-table {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.staging-table__head {
  flex: none;
  margin-bottom: 16px;
}

.staging-table__error {
  flex: none;
  margin-bottom: 14px;
}

/* The ONE scroll container — `min-height: 0` is the same flex-item overflow
 * trap PanelCard/Downloads document: without it this region refuses to
 * shrink below its content (every staged row) and the bounded ancestor above
 * would grow instead of scrolling internally. */
.staging-table__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.staging-table__rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.staging-table__more {
  flex: none;
  display: flex;
  justify-content: center;
  margin-top: 20px;
  padding-top: 4px;
}
</style>
