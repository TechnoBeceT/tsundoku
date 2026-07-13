import { describe, expect, it } from 'vitest'
import type { SeriesSummary } from '../screens/types'
import { countMatchesElsewhere, filterByCategory, filterNeedsSource, searchSeries } from './libraryFilter'

function series(over: Partial<SeriesSummary> & { id: string }): SeriesSummary {
  return {
    id: over.id,
    title: over.title ?? over.id,
    slug: over.slug ?? over.id,
    category: over.category ?? 'Manga',
    coverUrl: over.coverUrl ?? '',
    monitored: over.monitored ?? true,
    completed: over.completed ?? false,
    needsSource: over.needsSource ?? false,
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

describe('filterNeedsSource', () => {
  const mixed: SeriesSummary[] = [
    series({ id: 'a', needsSource: true }),
    series({ id: 'b', needsSource: false }),
    series({ id: 'c', needsSource: true }),
  ]

  it('active=false returns the whole list unchanged (same reference)', () => {
    expect(filterNeedsSource(mixed, false)).toBe(mixed)
  })

  it('active=true keeps only needsSource series', () => {
    expect(filterNeedsSource(mixed, true).map((s) => s.id)).toEqual(['a', 'c'])
  })

  it('active=true is cover-independent: a needsSource series WITH a cover is still kept', () => {
    const withCover = series({ id: 'z', needsSource: true, coverUrl: '/api/series/z/cover?v=abc' })
    expect(filterNeedsSource([withCover], true)).toEqual([withCover])
  })
})

describe('countMatchesElsewhere', () => {
  it('counts matches in OTHER categories, not the active one', () => {
    // ASYMMETRIC on purpose so the count PROVES the direction of the filter:
    //   "solo" INSIDE Manga  = c (Solo Bug Player)                        → 1
    //   "solo" OUTSIDE Manga = a (Solo Leveling), b (Solo Max-Level Newbie),
    //                          e (The Solo Farming)                       → 3
    // The correct answer (OUTSIDE) is 3; a mutation to `=== category`
    // (counting INSIDE) would return 1 → this assertion catches it. A symmetric
    // fixture (equal in/out) would pass either way and leave the escape hatch
    // untested — the whole reason the library loads all categories at once.
    const n = countMatchesElsewhere(all, 'Manga', 'solo')
    expect(n).toBe(3)
  })

  it('is 0 when the query is blank or the category is null', () => {
    expect(countMatchesElsewhere(all, 'Manhwa', '')).toBe(0)
    expect(countMatchesElsewhere(all, 'Manhwa', '   ')).toBe(0)
    expect(countMatchesElsewhere(all, null, 'solo')).toBe(0)
  })
})
