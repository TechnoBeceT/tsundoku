/**
 * sourceReport.types.ts — screen-facing types for the Kaizoku-grade Source
 * Metrics report (Source Health Console, slice 4). They mirror the backend
 * `GET /api/reporting/*` DTOs (generated in `utils/api/schema.d.ts`) with one
 * deliberate transformation: every OPTIONAL wire field is normalised to a
 * NULLABLE screen field (`errorMessage?: string` → `errorMessage: string | null`),
 * so a component never has to distinguish "absent" from "null" — the same posture
 * as `sourceHealth.types.ts`. No field is renamed (the report DTOs are already
 * clean camelCase); the composables map absent → null and pass the rest through.
 *
 * Per QCAT-021 the wire contract lives in the generated client; these are the
 * SCREEN mirror the presentational components speak, decoupled from the schema
 * import path.
 */

/** The reporting window. Drives every report fetch; owned by the report tab. */
export type ReportPeriod = '24h' | '7d' | '30d'

/** The per-source rollup ordering. */
export type ReportSort = 'failures' | 'latency' | 'events'

/** A source-operation type — the audit-log event categories. */
export type EventType = 'search' | 'download' | 'refresh' | 'warm' | 'breaker_trip' | 'breaker_reset'

/** An operation's binary outcome. */
export type EventStatus = 'success' | 'failed'

/** The histogram bucket granularity. */
export type TimelineBucketSize = 'hour' | 'day'

/** The headline numbers for a reporting window. */
export interface ReportKpis {
  /** How many source operations ran in the window. */
  totalEvents: number
  /** How many succeeded. */
  successEvents: number
  /** How many failed. */
  failedEvents: number
  /** successEvents / totalEvents as a 0..1 fraction (0 when none). */
  successRate: number
  /** How many distinct sources produced any event in the window. */
  activeSources: number
}

/** One operation type's success/fail tally for the window. */
export interface EventTypeTally {
  /** The source-operation type. */
  eventType: EventType
  /** How many events of this type ran. */
  total: number
  /** How many succeeded. */
  success: number
  /** How many failed. */
  failed: number
}

/** One entry of the slowest-sources leaderboard (rolling EWMA snapshot). */
export interface SlowSource {
  /** Canonical source key — the join key across events/metrics/breaker. */
  sourceKey: string
  /** The source's display name. */
  sourceName: string
  /** Exponentially-weighted rolling search latency, in milliseconds. */
  ewmaLatencyMs: number
}

/**
 * One entry of the currently-failing leaderboard, from the circuit-breaker state
 * — the authoritative "erroring since when" without an event-log scan.
 */
export interface FailingSource {
  /** Canonical source key. */
  sourceKey: string
  /** Start of the current failure streak (ISO 8601; null when not failing). */
  failingSince: string | null
  /** How many gated fetches failed in a row. */
  consecutiveFailures: number
  /** Most recent gated-fetch failure reason ("" when none). */
  lastError: string
  /** When the tripped breaker reopens (ISO 8601; null when not tripped). */
  cooldownUntil: string | null
  /** Derived — a cooldown is set and still in the future. */
  isCoolingDown: boolean
}

/**
 * One row of the append-only source-operation audit log. The three optional wire
 * fields are normalised to null here; `metadata` is always an object (possibly
 * empty).
 */
export interface SourceEventRecord {
  /** The event's unique id. */
  id: string
  /** Canonical source key. */
  sourceKey: string
  /** The numeric engine-host source id as a string ("" for a disk source). */
  sourceId: string
  /** The source's display name captured at write time. */
  sourceName: string
  /** The source's language code ("" when unknown). */
  language: string
  /** The source-operation type. */
  eventType: EventType
  /** The operation's binary outcome. */
  status: EventStatus
  /** The operation's wall-clock duration in milliseconds (0 when not timed). */
  durationMs: number
  /** The (truncated) failure reason; null on success. */
  errorMessage: string | null
  /** The classified error bucket (captcha/rate_limit/…); null on success. */
  errorCategory: string | null
  /** The operation's result cardinality where meaningful; null otherwise. */
  itemsCount: number | null
  /** Small operation-specific forensic context (keyword, url, chapter, series). */
  metadata: Record<string, string>
  /** The immutable event timestamp (ISO 8601). */
  createdAt: string
}

/**
 * The period dashboard: headline KPIs, the per-operation breakdown, the slowest +
 * currently-failing leaderboards, and a preview of the most recent errors.
 */
export interface ReportOverview {
  /** The window this report covers. */
  period: ReportPeriod
  /** The inclusive lower bound of the window (ISO 8601). */
  since: string
  /** The headline numbers. */
  kpis: ReportKpis
  /** Per-operation success/fail tally. */
  eventsByType: EventTypeTally[]
  /** The slowest sources by rolling EWMA latency. */
  slowestSources: SlowSource[]
  /** Sources currently in a failure streak, longest-failing first. */
  failingSources: FailingSource[]
  /** The most recent failed events in the window, newest first. */
  recentErrors: SourceEventRecord[]
}

/**
 * One source's rollup for the window: identity, overall + per-operation
 * success/fail tallies (event log), joined rolling latency (metrics), and the
 * current failure streak (breaker) — the per-source accordion's data.
 */
export interface SourceReport {
  /** Canonical source key. */
  sourceKey: string
  /** The numeric engine-host source id as a string ("" for a disk source). */
  sourceId: string
  /** The source's display name. */
  sourceName: string
  /** The source's language code ("" when unknown). */
  language: string
  /** How many operations ran in the window. */
  totalEvents: number
  /** How many succeeded. */
  successEvents: number
  /** How many failed. */
  failedEvents: number
  /** successEvents / totalEvents as a 0..1 fraction (0 when none). */
  successRate: number
  /** Per-operation breakdown, sorted by total descending. */
  byType: EventTypeTally[]
  /** Rolling EWMA search latency, in milliseconds (0 when never measured). */
  ewmaLatencyMs: number
  /** Most recent measured search latency, in milliseconds. */
  lastLatencyMs: number
  /** Start of the current failure streak (ISO 8601; null when not failing). */
  failingSince: string | null
  /** How many gated fetches failed in a row. */
  consecutiveFailures: number
  /** Most recent gated-fetch failure reason ("" when none). */
  lastError: string
  /** When the tripped breaker reopens (ISO 8601; null when not tripped). */
  cooldownUntil: string | null
  /** Derived — a cooldown is set and still in the future. */
  isCoolingDown: boolean
}

/** A page of the raw event feed plus the total matching count. */
export interface SourceEventPage {
  /** Total number of events matching the filter (ignores pagination). */
  total: number
  /** The page of events. */
  items: SourceEventRecord[]
}

/** One time slot's success/fail tally — the timeline histogram's datum. */
export interface TimelineBucket {
  /** The slot's start (ISO 8601 date_trunc boundary). */
  bucket: string
  /** Successful operations in the slot. */
  success: number
  /** Failed operations in the slot. */
  failed: number
  /** All operations in the slot. */
  total: number
}
