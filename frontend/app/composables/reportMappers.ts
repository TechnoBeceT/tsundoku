/**
 * reportMappers.ts — the shared DTO→screen mappers for the Source Metrics report.
 * The event record and the breaker "failing source" shapes are returned by more
 * than one endpoint (overview embeds recent errors + failing sources; the event
 * feed returns event records; the timeline is its own), so their optional→null
 * normalisation lives here once (§2 DRY) rather than being re-written in each
 * composable.
 */
import type { components } from '~/utils/api/schema.d.ts'
import type { FailingSource, SourceEventRecord } from '~/components/health/sourceReport.types'

type EventRecordDTO = components['schemas']['SourceEventRecord']
type FailingSourceDTO = components['schemas']['FailingSource']

/** Map an audit-log event DTO → screen record (three optionals → null). */
export function mapEventRecord(dto: EventRecordDTO): SourceEventRecord {
  return {
    id: dto.id,
    sourceKey: dto.sourceKey,
    sourceId: dto.sourceId,
    sourceName: dto.sourceName,
    language: dto.language,
    eventType: dto.eventType,
    status: dto.status,
    durationMs: dto.durationMs,
    errorMessage: dto.errorMessage ?? null,
    errorCategory: dto.errorCategory ?? null,
    itemsCount: dto.itemsCount ?? null,
    metadata: dto.metadata,
    createdAt: dto.createdAt,
  }
}

/** Map a breaker "failing source" DTO → screen shape (timestamps → null). */
export function mapFailingSource(dto: FailingSourceDTO): FailingSource {
  return {
    sourceKey: dto.sourceKey,
    failingSince: dto.failingSince ?? null,
    consecutiveFailures: dto.consecutiveFailures,
    lastError: dto.lastError,
    cooldownUntil: dto.cooldownUntil ?? null,
    isCoolingDown: dto.isCoolingDown,
  }
}
