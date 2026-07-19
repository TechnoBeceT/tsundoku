/**
 * useSourceEvents — the data layer for the raw event-log feed
 * (GET /api/reporting/source/{sourceKey}/events). Backs BOTH the global event log
 * at the bottom of the report (`sourceKey` = the "__all__" sentinel) and a single
 * source's recent-events list (a real source key), so the sentinel + the filter +
 * pagination logic live in one place.
 *
 * The feed is paginated + filtered SERVER-side (status, eventType, limit, offset),
 * so this composable renders exactly the page the API returns and never folds the
 * whole log client-side. `total` is the full match count (ignoring pagination) so
 * the UI can show "N events" and page without a second call. Changing a filter
 * resets the offset to 0 (a filtered view always starts at its first page).
 *
 * §16: `pending` + `error` are exposed; an empty result is `items: [], total: 0`.
 */
import { computed, ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import { mapEventRecord } from '~/composables/reportMappers'
import type { EventStatus, EventType, SourceEventRecord } from '~/components/health/sourceReport.types'

/** The "__all__" sentinel selects the cross-source global feed. */
export const ALL_SOURCES_KEY = '__all__'

export function useSourceEvents(options: {
  sourceKey?: string
  limit?: number
  immediate?: boolean
} = {}) {
  const { immediate = true } = options

  const sourceKey = ref<string>(options.sourceKey ?? ALL_SOURCES_KEY)
  const status = ref<EventStatus | ''>('')
  const eventType = ref<EventType | ''>('')
  const limit = ref<number>(options.limit ?? 50)
  const offset = ref<number>(0)

  const events = ref<SourceEventRecord[]>([])
  const total = ref<number>(0)
  const pending = ref(false)
  const error = ref<string | null>(null)

  /** The current 0-based page and the page count (≥1) — for the pager UI. */
  const page = computed(() => Math.floor(offset.value / limit.value))
  const pageCount = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))

  /** Load (or reload) the current page for the current source + filters. */
  async function refetch(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/reporting/source/{sourceKey}/events', {
        params: {
          path: { sourceKey: sourceKey.value },
          query: {
            status: status.value || undefined,
            eventType: eventType.value || undefined,
            limit: limit.value,
            offset: offset.value,
          },
        },
      })
      if (res.error || !res.data) throw new Error('Failed to load the event log')
      events.value = res.data.items.map(mapEventRecord)
      total.value = res.data.total
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load the event log'
    }
    finally {
      pending.value = false
    }
  }

  /** Point the feed at a different source (or "__all__") and reload from page 0. */
  function setSourceKey(next: string): void {
    sourceKey.value = next
    offset.value = 0
    void refetch()
  }

  /** Filter by outcome ('' = any) — resets to the first page. */
  function setStatus(next: EventStatus | ''): void {
    status.value = next
    offset.value = 0
    void refetch()
  }

  /** Filter by operation type ('' = any) — resets to the first page. */
  function setEventType(next: EventType | ''): void {
    eventType.value = next
    offset.value = 0
    void refetch()
  }

  /** Jump to a 0-based page (clamped to the available range) + reload. */
  function goToPage(next: number): void {
    const clamped = Math.min(Math.max(0, next), pageCount.value - 1)
    offset.value = clamped * limit.value
    void refetch()
  }

  if (immediate) void refetch()

  return {
    sourceKey,
    status,
    eventType,
    limit,
    offset,
    events,
    total,
    pending,
    error,
    page,
    pageCount,
    refetch,
    setSourceKey,
    setStatus,
    setEventType,
    goToPage,
  }
}
