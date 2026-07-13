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
 */
export type SortKey = 'title' | 'added' | 'updated' | 'unread'
export type SortDir = 'asc' | 'desc'

function cmpDate(a: string, b: string): number {
  return Date.parse(a) - Date.parse(b)
}

function compareBy(key: SortKey, a: SeriesSummary, b: SeriesSummary, sign: number): number {
  switch (key) {
    case 'title':
      return sign * a.title.localeCompare(b.title)
    case 'unread':
      return sign * (a.chapterCounts.unread - b.chapterCounts.unread)
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
  }
}

export function sortSeries(items: SeriesSummary[], key: SortKey, dir: SortDir): SeriesSummary[] {
  const sign = dir === 'asc' ? 1 : -1
  return [...items].sort((a, b) => {
    const c = compareBy(key, a, b, sign)
    if (c !== 0) return c
    return a.title.localeCompare(b.title) || a.id.localeCompare(b.id)
  })
}
