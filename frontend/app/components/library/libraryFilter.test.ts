import { describe, expect, it } from 'vitest'
import type { SeriesSummary } from '../screens/types'
import { countMatchesElsewhere, filterByCategory, searchSeries } from './libraryFilter'

function series(over: Partial<SeriesSummary> & { id: string }): SeriesSummary {
  return {
    id: over.id,
    title: over.title ?? over.id,
    slug: over.slug ?? over.id,
    category: over.category ?? 'Manga',
    coverUrl: over.coverUrl ?? '',
    monitored: over.monitored ?? true,
    completed: over.completed ?? false,
    chapterCounts: over.chapterCounts ?? {
      total: 0, downloaded: 0, wanted: 0, failed: 0, unread: 0,
    },
    createdAt: over.createdAt ?? '2020-01-01T00:00:00Z',
    lastChapterDownloadedAt: over.lastChapterDownloadedAt ?? null,
  }
}

const all: SeriesSummary[] = [
  series({ id: 'a', title: 'Solo Leveling', category: 'Manhwa' }),
  series({ id: 'b', title: 'Solo Max-Level Newbie', category: 'Manhwa' }),
  series({ id: 'c', title: 'Solo Bug Player', category: 'Manga' }),
  series({ id: 'd', title: 'Berserk', category: 'Manga' }),
  series({ id: 'e', title: 'The Solo Farming', category: 'Manhua' }),
]

describe('searchSeries', () => {
  it('search is case-insensitive and trimmed; blank matches everything', () => {
    expect(searchSeries(all, '  SOLO  ').map((s) => s.id)).toEqual(['a', 'b', 'c', 'e'])
    expect(searchSeries(all, '').map((s) => s.id)).toEqual(['a', 'b', 'c', 'd', 'e'])
    expect(searchSeries(all, '   ').map((s) => s.id)).toEqual(['a', 'b', 'c', 'd', 'e'])
  })

  it('returns the same array reference for a blank query (no needless copy)', () => {
    expect(searchSeries(all, '')).toBe(all)
  })
})

describe('filterByCategory', () => {
  it('null returns all; a name returns only that category', () => {
    expect(filterByCategory(all, null)).toBe(all)
    expect(filterByCategory(all, 'Manhwa').map((s) => s.id)).toEqual(['a', 'b'])
    expect(filterByCategory(all, 'Manga').map((s) => s.id)).toEqual(['c', 'd'])
    expect(filterByCategory(all, 'Nonexistent')).toEqual([])
  })
})

describe('countMatchesElsewhere', () => {
  it('counts matches in OTHER categories, not the active one', () => {
    // "solo" in categories != Manhwa: c (Solo Bug Player, Manga), e (The Solo Farming, Manhua) = 2.
    // If computed against the ACTIVE category it could only ever return 0 → silently dead.
    const n = countMatchesElsewhere(all, 'Manhwa', 'solo')
    expect(n).toBe(2)
  })

  it('is 0 when the query is blank or the category is null', () => {
    expect(countMatchesElsewhere(all, 'Manhwa', '')).toBe(0)
    expect(countMatchesElsewhere(all, 'Manhwa', '   ')).toBe(0)
    expect(countMatchesElsewhere(all, null, 'solo')).toBe(0)
  })
})
