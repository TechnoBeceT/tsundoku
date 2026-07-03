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
import type { components } from '~/utils/api/schema.d.ts'
import type { SearchCandidate, SearchGroup } from '~/components/screens/import.types'

type SearchCandidateDTO = components['schemas']['SearchCandidate']
type SearchGroupDTO = components['schemas']['SearchGroup']

/** Maps one backend SearchCandidate DTO onto the shared screen type. */
export function mapCandidate(dto: SearchCandidateDTO): SearchCandidate {
  return {
    source: dto.source,
    sourceName: dto.sourceName,
    lang: dto.lang,
    mangaId: dto.mangaId,
    title: dto.title,
    thumbnailUrl: dto.thumbnailUrl,
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
