/**
 * useSourceTimeline — the data layer for the per-source stacked success/fail
 * histogram (GET /api/reporting/source/{sourceKey}/timeline). The backend
 * pre-buckets the counts by hour or day via date_trunc, so this composable just
 * fetches the ready-made series — it never buckets raw events client-side.
 *
 * Used per accordion row: when a source is expanded the first time, its timeline
 * is loaded once via `load(sourceKey, bucket, period)`. The "__all__" sentinel
 * yields the cross-source timeline (available if a caller wants a global cliff
 * view). §16: `pending` + `error` are exposed; an empty series is `buckets: []`.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type {
  ReportPeriod,
  TimelineBucket,
  TimelineBucketSize,
} from '~/components/health/sourceReport.types'

export function useSourceTimeline() {
  const buckets = ref<TimelineBucket[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  /**
   * Load the bucketed success/fail series for a source. `bucket` picks the
   * granularity (hour for 24h, day for 7d/30d is the sensible pairing) and
   * `period` the window. Replaces the current series.
   */
  async function load(
    sourceKey: string,
    bucket: TimelineBucketSize = 'hour',
    period: ReportPeriod = '24h',
  ): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/reporting/source/{sourceKey}/timeline', {
        params: { path: { sourceKey }, query: { bucket, period } },
      })
      if (res.error || !res.data) throw new Error('Failed to load the timeline')
      buckets.value = res.data.buckets
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load the timeline'
      buckets.value = []
    }
    finally {
      pending.value = false
    }
  }

  return { buckets, pending, error, load }
}
