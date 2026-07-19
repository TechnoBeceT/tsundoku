/**
 * eventType.ts — display metadata for the source-operation event types. The
 * breakdown, the event table, and the log filter all label the same six
 * `event_type` values, so the human labels + the filter option list live here
 * once (§2 DRY) rather than being re-declared per component.
 *
 * 🔴 The KEYS must stay byte-identical to the backend `event_type` enum — they
 * are the stored/queried values.
 */
import type { EventType } from '~/components/health/sourceReport.types'

/** The human label for each source-operation type. */
export const EVENT_TYPE_LABELS: Record<EventType, string> = {
  search: 'Search',
  download: 'Download',
  refresh: 'Refresh',
  warm: 'Warm-up',
  breaker_trip: 'Breaker trip',
  breaker_reset: 'Breaker reset',
}

/** Resolve an event-type key to its label (falls back to the raw key). */
export function eventTypeLabel(type: string): string {
  return (EVENT_TYPE_LABELS as Record<string, string>)[type] ?? type
}

/**
 * The ordered `{ value, label }` options for the event-type filter dropdown,
 * with an "any type" first entry (empty value = no filter).
 */
export const EVENT_TYPE_FILTER_OPTIONS: { value: string, label: string }[] = [
  { value: '', label: 'All operations' },
  ...(Object.keys(EVENT_TYPE_LABELS) as EventType[]).map(k => ({ value: k, label: EVENT_TYPE_LABELS[k] })),
]

/** The ordered `{ value, label }` options for the status filter dropdown. */
export const STATUS_FILTER_OPTIONS: { value: string, label: string }[] = [
  { value: '', label: 'Any outcome' },
  { value: 'success', label: 'Success' },
  { value: 'failed', label: 'Failed' },
]
