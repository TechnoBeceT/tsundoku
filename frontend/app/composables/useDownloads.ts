/**
 * useDownloads — data layer for the downloads screen.
 *
 * Fetches GET /api/downloads?state=<all-relevant-states> once; the screen
 * derives the Active / Failed / Queued tab views itself from the flat list.
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
 */
import { ref, onUnmounted } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { DownloadItem, DownloadTab, RetryAllState, ErrorCategory } from '~/components/screens/downloads.types'

type DownloadChapterDTO = components['schemas']['DownloadChapter']

// Union of all states the Downloads screen renders (Active + Failed + Queued tabs).
const ALL_STATES = 'wanted,downloading,upgrading,upgrade_available,failed,permanently_failed'

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

  // cycleActive: forwarded from the module-singleton SSE composable.
  const { cycleActive, on } = useProgressStream()

  // No event-driven next-cycle countdown is available — be honest rather than
  // guessing. The screen hides the countdown pill when this is null.
  const nextCycleMinutes: number | null = null

  async function refresh(): Promise<void> {
    loading.value = true
    try {
      const res = await apiClient.GET('/api/downloads', {
        params: { query: { state: ALL_STATES } },
      })
      if (res.error || !res.data) throw new Error('Failed to load downloads')
      items.value = res.data.items.map(mapItem)
    }
    catch (e) {
      retryError.value = e instanceof Error ? e.message : 'Failed to load downloads'
    }
    finally {
      loading.value = false
    }
  }

  // Auto-refetch whenever a download cycle completes or a chapter download finishes.
  const unsubCycleDone = on('cycle.done', () => void refresh())
  const unsubDownloadDone = on('download.done', () => void refresh())

  onUnmounted(() => {
    unsubCycleDone()
    unsubDownloadDone()
  })

  function setTab(tab: DownloadTab): void {
    activeTab.value = tab
  }

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

  void refresh()

  return {
    items,
    activeTab,
    loading,
    retryingIds,
    retryingAll,
    retryError,
    cycleActive,
    nextCycleMinutes,
    setTab,
    retry,
    retryAll,
    dismissError,
    refresh,
  }
}
