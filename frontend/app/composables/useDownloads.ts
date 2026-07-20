/**
 * useDownloads — data layer for the downloads screen.
 *
 * Per-tab paginated loader: fetches GET /api/downloads?state=<tab-states>&limit=50&offset=N
 * for the active tab, plus 4 parallel count probes (limit:1) for exact badge counts.
 * Switching tabs resets to offset 0; loadMore() appends the next page.
 *
 * Maps the generated DownloadChapter DTO → DownloadItem, unwrapping the
 * { total, items } envelope.
 *
 * Field renames (DTO → screen):
 *   id              → chapterId
 *   seriesCoverUrl  → coverUrl
 *   nextAttemptAt   → nextAttempt (formatted: "now" / "in 12m" / "in 2h"; null → undefined)
 *
 * §16 mutations:
 *   retry(chapterId)   — POST /api/chapters/{id}/retry; tracks id in retryingIds
 *   retryAll(state)    — POST /api/downloads/retry-all?state=; tracks retryingAll
 *   Both refetch on success; failure sets retryError (dismissible via dismissError).
 *
 * Auto-refetches on cycle.done and download.done SSE events so the list stays
 * current while a download cycle is active. cycleActive is forwarded from
 * useProgressStream (true on cycle.start, false on cycle.done).
 *
 * "Download now": runNow() — POST /api/downloads/run; mirrors useSourceMetrics'
 * warmNow() §16 pattern (busy → started-message, never swallowed). The endpoint
 * returns 202 immediately (the cycle runs via the existing SSE-driven job), so
 * this does not refetch itself — the cycle.done listener above already refetches
 * once the triggered cycle completes.
 *
 * Documented caveat: client-side search / fail sub-tabs / upgrades-only toggle
 * operate over the *loaded* pages of the active tab. Tab badge counts + bulk gating
 * are exact (server totals). Pushing search to the server (q param) is a deliberate
 * future add.
 */
import { ref, computed, onUnmounted } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { DownloadItem, DownloadTab, RetryAllState, ErrorCategory } from '~/components/screens/downloads.types'

type DownloadChapterDTO = components['schemas']['DownloadChapter']

// Page size for all tab fetches (backend cap is 200).
const PAGE = 50

// State strings for each top-level tab, passed directly to ?state= on the API.
const TAB_STATES: Record<DownloadTab, string> = {
  active: 'downloading,upgrading',
  failed: 'failed,permanently_failed',
  queued: 'wanted,upgrade_available',
}

/**
 * Format an ISO 8601 next-attempt timestamp into a short human string:
 *   "now"   — timestamp is in the past or ≤ 0 ms away
 *   "in Nm" — less than 60 minutes away
 *   "in Nh" — 60+ minutes away
 * Returns undefined for null (no backoff scheduled).
 */
function formatNextAttempt(isoString: string | null): string | undefined {
  if (!isoString) return undefined
  const diffMs = new Date(isoString).getTime() - Date.now()
  if (diffMs <= 0) return 'now'
  const diffMin = Math.round(diffMs / 60_000)
  if (diffMin < 60) return `in ${diffMin}m`
  return `in ${Math.round(diffMs / 3_600_000)}h`
}

// Set of valid ErrorCategory values; used to safely narrow the DTO's `errorCategory: string`.
const VALID_ERROR_CATEGORIES = new Set<string>(['network', 'source', 'cloudflare', 'timeout', 'parse'])

function mapItem(dto: DownloadChapterDTO): DownloadItem {
  return {
    chapterId: dto.id,
    seriesId: dto.seriesId,
    seriesTitle: dto.seriesTitle,
    seriesCategory: dto.seriesCategory,
    coverUrl: dto.seriesCoverUrl,
    number: dto.number,
    name: dto.name,
    // The DTO state includes 'downloaded' (filtered out by the query), so narrow safely.
    state: dto.state as DownloadItem['state'],
    provider: dto.provider,
    providerName: dto.providerName,
    // "" means "not upgrading / no nameable target" — map to undefined so the row
    // simply omits the "→ target" half.
    upgradeTarget: dto.upgradeTarget || undefined,
    // The waited-on source's cooldown: raw ISO through to the row (which counts it
    // down live). null (not deferred) → undefined so the row shows no waiting note.
    deferredUntil: dto.deferredUntil ?? undefined,
    deferReason: dto.deferReason || undefined,
    // Backend "" (not waiting) → undefined so a ready row shows no chip.
    waitingReason: dto.waitingReason === 'backoff' || dto.waitingReason === 'cooling_down'
      ? dto.waitingReason
      : undefined,
    attempts: dto.attempts ?? 0,
    maxRetries: dto.maxRetries ?? 0,
    isUpgrade: dto.isUpgrade ?? false,
    retries: dto.retries,
    nextAttempt: formatNextAttempt(dto.nextAttemptAt),
    // Empty string means "no error" — map to undefined so optional fields stay absent.
    lastError: dto.lastError || undefined,
    errorCategory: VALID_ERROR_CATEGORIES.has(dto.errorCategory)
      ? (dto.errorCategory as ErrorCategory)
      : undefined,
  }
}

export function useDownloads() {
  const items = ref<DownloadItem[]>([])
  const loading = ref(false)
  const activeTab = ref<DownloadTab>('active')
  const retryingIds = ref<string[]>([])
  const retryingAll = ref<RetryAllState | null>(null)
  const retryError = ref<string>('')

  // §16 state of the manual "Download now" action.
  const running = ref(false)
  const runMessage = ref<string>('')
  const runError = ref<string>('')

  // Pagination state
  const total = ref(0)
  const offset = ref(0)
  const loadingMore = ref(false)

  // Exact per-state badge counts from 4 parallel server probes.
  const counts = ref({ active: 0, failed: 0, terminal: 0, queued: 0 })

  // More results exist when the loaded page is shorter than the server total.
  const hasMore = computed(() => items.value.length < total.value)

  // cycleActive: forwarded from the module-singleton SSE composable.
  const { cycleActive, on } = useProgressStream()

  // No event-driven next-cycle countdown is available — be honest rather than
  // guessing. The screen hides the countdown pill when this is null.
  const nextCycleMinutes: number | null = null

  /**
   * Fetch one page of the given tab's chapters.
   *   append=false: reset offset + items → full tab reload.
   *   append=true:  read current offset → append results (loadMore path).
   */
  async function loadTab(tab: DownloadTab, append: boolean): Promise<void> {
    if (append) {
      loadingMore.value = true
    }
    else {
      loading.value = true
      offset.value = 0
      total.value = 0 // Reset so hasMore is false while the new page loads.
    }
    try {
      const res = await apiClient.GET('/api/downloads', {
        params: { query: { state: TAB_STATES[tab], limit: PAGE, offset: offset.value } },
      })
      if (res.error || !res.data) throw new Error('Failed to load downloads')
      const mapped = res.data.items.map(mapItem)
      items.value = append ? [...items.value, ...mapped] : mapped
      total.value = res.data.total
    }
    catch (e) {
      retryError.value = e instanceof Error ? e.message : 'Failed to load downloads'
    }
    finally {
      loading.value = false
      loadingMore.value = false
    }
  }

  /**
   * 4 parallel count probes (limit:1, read only total) so tab badges + bulk-action
   * gating are exact server totals — not derived from the loaded page subset.
   */
  async function loadCounts(): Promise<void> {
    const probe = async (state: string): Promise<number> => {
      const res = await apiClient.GET('/api/downloads', { params: { query: { state, limit: 1 } } })
      return res.data?.total ?? 0
    }
    const [active, failed, terminal, queued] = await Promise.all([
      probe('downloading,upgrading'),
      probe('failed'),
      probe('permanently_failed'),
      probe('wanted,upgrade_available'),
    ])
    counts.value = { active, failed, terminal, queued }
  }

  /** Full reload of the active tab + refresh all badge counts. */
  async function refresh(): Promise<void> {
    await Promise.all([loadTab(activeTab.value, false), loadCounts()])
  }

  /** Load the next page of the active tab and append the results. */
  async function loadMore(): Promise<void> {
    offset.value += PAGE
    await loadTab(activeTab.value, true)
  }

  /** Switch to a tab and load its first page (offset 0). */
  function setTab(tab: DownloadTab): void {
    activeTab.value = tab
    void loadTab(tab, false)
  }

  // Auto-refetch whenever a download cycle completes or a chapter download finishes.
  const unsubCycleDone = on('cycle.done', () => void refresh())
  const unsubDownloadDone = on('download.done', () => void refresh())
  // A chapter just failed — pull the fresh Failed list + counts so the row (and its
  // last_error) appears without waiting for the coarse cycle.done refetch.
  const unsubDownloadFail = on('download.fail', () => void refresh())

  // Live per-page progress: update the matching row IN PLACE (no refetch) so the
  // Active bar climbs smoothly between the coarse start/done refetches. The SSE
  // payload is snake_case (mirrors the backend DownloadEvent json tags).
  const unsubProgress = on('download.progress', (data) => {
    const p = data as { chapter_id?: string, current?: number, total?: number }
    if (!p.chapter_id || !p.total || p.total <= 0) return // guard unknown id / divide-by-zero
    const row = items.value.find((i) => i.chapterId === p.chapter_id)
    if (!row) return // progress for a chapter not on the loaded page — ignore
    row.progress = Math.round((100 * (p.current ?? 0)) / p.total)
    row.pagesCurrent = p.current
    row.pagesTotal = p.total
  })

  onUnmounted(() => {
    unsubCycleDone()
    unsubDownloadDone()
    unsubDownloadFail()
    unsubProgress()
  })

  async function retry(chapterId: string): Promise<void> {
    if (retryingIds.value.includes(chapterId)) return
    retryingIds.value = [...retryingIds.value, chapterId]
    retryError.value = ''
    try {
      const res = await apiClient.POST('/api/chapters/{id}/retry', {
        params: { path: { id: chapterId } },
      })
      if (res.error) throw new Error('Retry failed')
      await refresh()
    }
    catch (e) {
      retryError.value = e instanceof Error ? e.message : 'Retry failed'
    }
    finally {
      retryingIds.value = retryingIds.value.filter((id) => id !== chapterId)
    }
  }

  async function retryAll(state: RetryAllState): Promise<void> {
    retryingAll.value = state
    retryError.value = ''
    try {
      const res = await apiClient.POST('/api/downloads/retry-all', {
        params: { query: { state } },
      })
      if (res.error) throw new Error('Retry all failed')
      await refresh()
    }
    catch (e) {
      retryError.value = e instanceof Error ? e.message : 'Retry all failed'
    }
    finally {
      retryingAll.value = null
    }
  }

  function dismissError(): void {
    retryError.value = ''
  }

  /**
   * Kick off an immediate download cycle ("Download now") — POST
   * /api/downloads/run. The endpoint returns 202 immediately (the cycle runs
   * via the existing job.Runner + SSE stream), so this only surfaces a
   * "started" message; the cycle.done listener above refetches once the
   * triggered cycle actually finishes. A failure lands in runError, never
   * swallowed (§16).
   */
  async function runNow(): Promise<void> {
    running.value = true
    runMessage.value = ''
    runError.value = ''
    try {
      const res = await apiClient.POST('/api/downloads/run')
      if (res.error) throw new Error('Failed to start download cycle')
      runMessage.value = 'Download cycle started'
    }
    catch (e) {
      runError.value = e instanceof Error ? e.message : 'Failed to start download cycle'
    }
    finally {
      running.value = false
    }
  }

  void refresh()

  return {
    items,
    activeTab,
    loading,
    total,
    hasMore,
    loadingMore,
    counts,
    retryingIds,
    retryingAll,
    retryError,
    cycleActive,
    nextCycleMinutes,
    running,
    runMessage,
    runError,
    setTab,
    loadMore,
    retry,
    retryAll,
    runNow,
    dismissError,
    refresh,
  }
}
