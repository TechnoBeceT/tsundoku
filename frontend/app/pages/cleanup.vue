<script setup lang="ts">
/**
 * Cleanup page — route "/cleanup". The composition root for the 3-tab Cleanup
 * console (Fractionals · Sourceless · Duplicates). It owns the active-tab state
 * and wires each tab's composable; the presentational Cleanup screen renders the
 * tabs and forwards actions.
 *
 * Tab state:
 *   - Resolved on mount from the `?tab=` deep-link, else the persisted session
 *     tab, else the `fractionals` default (resolveInitialCleanupTab). The two
 *     legacy routes `/fractionals` and `/sourceless` REDIRECT here carrying their
 *     tab in the query, so old bookmarks land on the right tab.
 *   - Persisted to sessionStorage on every change so returning to /cleanup
 *     reopens the last-used tab within the session.
 *
 * 🔴 Lazy data — load-bearing here, not an optimisation. EVERY tab is a
 * library-wide scan (the Duplicates one reads every series folder on disk), so
 * all three composables are created with `{ immediate: false }` and a tab is
 * fetched only the FIRST time it is shown. Opening Fractionals must never trigger
 * the duplicates scan. The watcher runs immediately, so the RESOLVED initial tab
 * (which may be any of the three, via a deep-link) loads on mount and the others
 * do not.
 *
 * The two cleanup dialogs live HERE, not in the screens, for the same reason they
 * do on the standalone pages: only the page learns whether a removal succeeded, so
 * it closes the dialog ONLY on success and shows the failure inside it otherwise
 * (§16).
 *
 * useFractionals / useSourceless / useDuplicateFiles are auto-imported from
 * app/composables/.
 */
import { computed, ref, watch } from 'vue'
import {
  CLEANUP_TAB_SESSION_KEY,
  resolveInitialCleanupTab,
  type CleanupTab,
} from '~/utils/cleanupTabs'
import type { FractionalCleanupPreview } from '~/components/screens/seriesDetail.types'
import type { SourcelessCleanupPreview } from '~/components/screens/sourceless.types'

// ── Tab data (ALL deferred — each is a library-wide scan) ─────────────────────
const fractionals = useFractionals({ immediate: false })
const sourceless = useSourceless({ immediate: false })
const duplicates = useDuplicateFiles({ immediate: false })

// ── Active tab: ?tab= deep-link → sessionStorage → default 'fractionals' ──────
const route = useRoute()
const queryTab = typeof route.query.tab === 'string' ? route.query.tab : null
const storedTab = import.meta.client ? sessionStorage.getItem(CLEANUP_TAB_SESSION_KEY) : null
const activeTab = ref<CleanupTab>(resolveInitialCleanupTab(queryTab, storedTab))

/** Update the active tab (called by @set-tab from the Cleanup shell). */
function setTab(tab: CleanupTab): void {
  activeTab.value = tab
}

// Persist every change so the tab survives navigating away and back.
watch(activeTab, (tab) => {
  if (import.meta.client) sessionStorage.setItem(CLEANUP_TAB_SESSION_KEY, tab)
})

// Load each tab's data exactly once, the first time that tab is shown (fires
// immediately for the resolved initial tab).
const loaded = new Set<CleanupTab>()
watch(activeTab, (tab) => {
  if (loaded.has(tab)) return
  loaded.add(tab)
  if (tab === 'fractionals') void fractionals.refetch()
  else if (tab === 'sourceless') void sourceless.refetch()
  else void duplicates.refetch()
}, { immediate: true })

/** Rescan whichever tab asked for it. */
function refreshTab(tab: CleanupTab): void {
  if (tab === 'fractionals') fractionals.refresh()
  else if (tab === 'sourceless') sourceless.refresh()
  else void duplicates.refresh()
}

// ── The per-tab prop bundles the shell renders ────────────────────────────────
const fractionalsPane = computed(() => ({
  series: fractionals.series.value,
  loading: fractionals.pending.value,
  refreshing: fractionals.refreshing.value,
  error: fractionals.error.value,
  busyIds: fractionals.togglingIds.value,
  toggleError: fractionals.toggleError.value,
}))

const sourcelessPane = computed(() => ({
  series: sourceless.series.value,
  loading: sourceless.pending.value,
  refreshing: sourceless.refreshing.value,
  error: sourceless.error.value,
}))

const duplicatesPane = computed(() => ({
  series: duplicates.series.value,
  totalFiles: duplicates.totalFiles.value,
  totalBytes: duplicates.totalBytes.value,
  loading: duplicates.pending.value,
  refreshing: duplicates.refreshing.value,
  error: duplicates.error.value,
}))

// ── Fractional cleanup dialog (owned by the page, per §16) ────────────────────
const fractionalOpen = ref(false)
const fractionalSeriesId = ref<string | null>(null)
const fractionalPreview = ref<FractionalCleanupPreview | null>(null)

async function openFractionalCleanup(seriesId: string): Promise<void> {
  fractionalSeriesId.value = seriesId
  fractionalPreview.value = await fractionals.fetchPreview(seriesId)
  fractionalOpen.value = true
}

async function confirmFractionalCleanup(chapterIds: string[]): Promise<void> {
  if (!fractionalSeriesId.value) return
  if (await fractionals.removeFractionals(fractionalSeriesId.value, chapterIds)) {
    fractionalOpen.value = false
  }
}

// ── Sourceless cleanup dialog (owned by the page, per §16) ────────────────────
const sourcelessOpen = ref(false)
const sourcelessSeriesId = ref<string | null>(null)
const sourcelessPreview = ref<SourcelessCleanupPreview | null>(null)

const sourcelessTitle = computed(() =>
  sourceless.series.value.find((s) => s.seriesId === sourcelessSeriesId.value)?.displayName ?? '',
)

async function openSourcelessCleanup(seriesId: string): Promise<void> {
  sourcelessSeriesId.value = seriesId
  sourcelessPreview.value = await sourceless.fetchPreview(seriesId)
  sourcelessOpen.value = true
}

async function confirmSourcelessCleanup(chapterIds: string[]): Promise<void> {
  if (!sourcelessSeriesId.value) return
  if (await sourceless.removeSourceless(sourcelessSeriesId.value, chapterIds)) {
    sourcelessOpen.value = false
  }
}
</script>

<template>
  <div class="page-cleanup">
    <Cleanup
      :active-tab="activeTab"
      :fractionals="fractionalsPane"
      :sourceless="sourcelessPane"
      :duplicates="duplicatesPane"
      @set-tab="setTab"
      @open-series="(id: string) => navigateTo(`/series/${id}`)"
      @refresh="refreshTab"
      @toggle-ignore="(p: { seriesId: string, ignore: boolean }) => fractionals.setIgnoreForSeries(p.seriesId, p.ignore)"
      @clean-files="openFractionalCleanup"
      @review-sourceless="openSourcelessCleanup"
    />

    <FractionalCleanupDialog
      v-model:open="fractionalOpen"
      :chapters="fractionalPreview?.chapters ?? []"
      :typical-page-count="fractionalPreview?.typicalPageCount ?? 0"
      :busy="fractionals.removeBusy.value"
      :error="fractionals.removeError.value"
      @confirm="confirmFractionalCleanup"
    />

    <SourcelessCleanupDialog
      :open="sourcelessOpen"
      :series-title="sourcelessTitle"
      :preview="sourcelessPreview"
      :busy="sourceless.removeBusy.value"
      :error="sourceless.removeError.value"
      @confirm="confirmSourcelessCleanup"
      @close="sourcelessOpen = false"
    />
  </div>
</template>

<style scoped>
.page-cleanup {
  min-height: 100%;
}
</style>
