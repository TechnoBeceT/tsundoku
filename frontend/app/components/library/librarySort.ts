import type { SeriesSummary } from '../screens/types'

/**
 * Pure sort kernel for the library grid.
 *
 * TWO RULES ARE LOAD-BEARING:
 *  1. EVERY sort ends in a deterministic tiebreak (title, then id). Equal values
 *     are routine (unread is 0 for dozens of series); without a tiebreak, equal-
 *     ranked series swap places on every re-render.
 *  2. NULLS SORT LAST IN BOTH DIRECTIONS — so the null check lives OUTSIDE the
 *     direction sign. Applying the sign to a nulls-last comparator would put nulls
 *     FIRST when descending.
 *
 * RANDOM is a special case: a stable per-render shuffle. The order must NOT
 * reshuffle when an unrelated input (search/filter) changes, so it is derived
 * deterministically from `hashSeed(id, seed)` — the CALLER bumps `seed` when it
 * wants a fresh shuffle (e.g. re-selecting the Random option).
 */
export type SortKey = 'title' | 'added' | 'updated' | 'waiting' | 'unread' | 'total' | 'random'
export type SortDir = 'asc' | 'desc'

function cmpDate(a: string, b: string): number {
  return Date.parse(a) - Date.parse(b)
}

/**
 * Deterministic string→uint32 hash (FNV-1a). Same input ⇒ same output within and
 * across renders, so a Random sort keyed on `id + seed` is stable until the seed
 * changes. Not cryptographic — a well-distributed shuffle is all that's needed.
 */
function hashSeed(id: string, seed: number): number {
  let h = 0x811c9dc5 ^ (seed >>> 0)
  const s = `${id}:${seed}`
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

function compareBy(key: SortKey, a: SeriesSummary, b: SeriesSummary, sign: number, seed: number): number {
  switch (key) {
    case 'title':
      return sign * a.title.localeCompare(b.title)
    case 'unread':
      return sign * (a.chapterCounts.unread - b.chapterCounts.unread)
    case 'total':
      return sign * (a.chapterCounts.total - b.chapterCounts.total)
    case 'random':
      return sign * (hashSeed(a.id, seed) - hashSeed(b.id, seed))
    case 'added':
      return sign * cmpDate(a.createdAt, b.createdAt)
    case 'updated': {
      const x = a.lastChapterDownloadedAt
      const y = b.lastChapterDownloadedAt
      if (x === null && y === null) return 0
      if (x === null) return 1 // nulls last — deliberately NOT multiplied by sign
      if (y === null) return -1
      return sign * cmpDate(x, y)
    }
    case 'waiting': {
      // Over latestChapterAt (the source's release date, QCAT-297): desc =
      // recently released, asc = longest waiting. Nulls sort last in BOTH
      // directions (same rule as 'updated' — the null branch is OUTSIDE the sign).
      const x = a.latestChapterAt
      const y = b.latestChapterAt
      if (x === null && y === null) return 0
      if (x === null) return 1
      if (y === null) return -1
      return sign * cmpDate(x, y)
    }
  }
}

export function sortSeries(items: SeriesSummary[], key: SortKey, dir: SortDir, seed = 0): SeriesSummary[] {
  const sign = dir === 'asc' ? 1 : -1
  return [...items].sort((a, b) => {
    const c = compareBy(key, a, b, sign, seed)
    if (c !== 0) return c
    return a.title.localeCompare(b.title) || a.id.localeCompare(b.id)
  })
}

/**
 * The canonical default DIRECTION for a freshly-picked sort field. Alphabetical
 * reads best A→Z; the count/date fields read best "most/newest first"; random is
 * direction-agnostic (ascending is fine). Selecting a field applies this; the
 * explicit asc/desc toggle then overrides it.
 */
export function defaultDirFor(key: SortKey): SortDir {
  return key === 'title' ? 'asc' : 'desc'
}
