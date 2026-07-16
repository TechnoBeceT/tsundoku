/**
 * importMappers — shared DTO → screen-type mappers for cross-source search
 * results.
 *
 * `GET /api/search` (the Adopt wizard, `useImport`) and
 * `GET /api/library/imports/match` (the Scan-Library wizard's "match a
 * source" step, `useScanLibrary`) both return the IDENTICAL `SearchGroup` /
 * `SearchCandidate` DTO shape — this file is the single home for mapping
 * that DTO onto the shared `import.types` screen types, so neither
 * composable re-implements the same mapping (§2 DRY).
 */
import { sourceCoverProxyUrl } from '~/utils/sourceCover'
import type { components } from '~/utils/api/schema.d.ts'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '~/components/screens/import.types'

type SearchCandidateDTO = components['schemas']['SearchCandidate']
type SearchGroupDTO = components['schemas']['SearchGroup']
type ScanlatorCoverageDTO = components['schemas']['ScanlatorCoverage']

/**
 * Maps one backend SearchCandidate DTO onto the shared screen type. Every
 * caller of this mapper (useImport, useMatchSource, useMatchDiskProvider,
 * useScanLibrary) picks up the source-cover proxy for free from this one spot
 * (§2 DRY) — see sourceCoverProxyUrl's doc comment for why the raw
 * thumbnailUrl is never used directly.
 */
export function mapCandidate(dto: SearchCandidateDTO): SearchCandidate {
  return {
    source: dto.source,
    sourceName: dto.sourceName,
    lang: dto.lang,
    mangaId: dto.mangaId,
    // The engine host addresses a manga by URL, not mangaId (P2 Suwayomi-removal)
    // — every adopt/add-source/match request must carry this back.
    url: dto.url,
    title: dto.title,
    thumbnailUrl: sourceCoverProxyUrl(dto.source, dto.thumbnailUrl),
  }
}

/**
 * Maps one backend SearchGroup DTO (a set of cross-source candidates the
 * backend matched as the same series) onto the shared screen type.
 */
export function mapGroup(dto: SearchGroupDTO): SearchGroup {
  return {
    title: dto.title,
    candidates: dto.candidates.map(mapCandidate),
  }
}

/**
 * Maps one backend ScanlatorCoverage DTO (from the per-scanlator breakdown
 * endpoint) onto the shared screen type. Reused by `useImport.loadBreakdowns`
 * (Adopt wizard auto-split) — the sole consumer today, kept here alongside the
 * other DTO mappers per this file's single-home convention.
 */
export function mapScanlatorCoverage(dto: ScanlatorCoverageDTO): ScanlatorCoverage {
  return {
    scanlator: dto.scanlator,
    count: dto.count,
    ranges: dto.ranges,
  }
}
