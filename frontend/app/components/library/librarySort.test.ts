import { describe, expect, it } from 'vitest'
import type { SeriesSummary } from '../screens/types'
import { sortSeries } from './librarySort'

/** Minimal SeriesSummary factory — only the fields the sort kernel reads matter. */
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
    latestChapterAt: over.latestChapterAt ?? null,
    isStalled: over.isStalled ?? false,
  }
}

/** Deterministic-seed-free shuffle — good enough to jostle input order between runs. */
function shuffle<T>(arr: T[]): T[] {
  const out = [...arr]
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[out[i], out[j]] = [out[j]!, out[i]!]
  }
  return out
}

const items: SeriesSummary[] = [
  series({ id: 'a', title: 'Alpha', createdAt: '2021-01-01T00:00:00Z', lastChapterDownloadedAt: '2023-05-01T00:00:00Z', chapterCounts: { total: 10, downloaded: 5, wanted: 5, failed: 0, unread: 3 } }),
  series({ id: 'b', title: 'Bravo', createdAt: '2022-06-01T00:00:00Z', lastChapterDownloadedAt: null, chapterCounts: { total: 8, downloaded: 8, wanted: 0, failed: 0, unread: 0 } }),
  series({ id: 'c', title: 'Charlie', createdAt: '2020-03-01T00:00:00Z', lastChapterDownloadedAt: '2024-11-01T00:00:00Z', chapterCounts: { total: 20, downloaded: 20, wanted: 0, failed: 0, unread: 0 } }),
  series({ id: 'd', title: 'Delta', createdAt: '2023-09-01T00:00:00Z', lastChapterDownloadedAt: '2022-01-01T00:00:00Z', chapterCounts: { total: 12, downloaded: 6, wanted: 6, failed: 0, unread: 7 } }),
  series({ id: 'e', title: 'Echo', createdAt: '2021-12-01T00:00:00Z', lastChapterDownloadedAt: null, chapterCounts: { total: 4, downloaded: 4, wanted: 0, failed: 0, unread: 0 } }),
]

describe('sortSeries', () => {
  it('is deterministic when values tie — equal unread counts never swap', () => {
    // unread is 0 for dozens of real series, so ties are ROUTINE, not hypothetical.
    // Without a stable tiebreak, equal-ranked series swap position on every re-render.
    const a = sortSeries(shuffle(items), 'unread', 'desc').map((s) => s.id)
    const b = sortSeries(shuffle(items), 'unread', 'desc').map((s) => s.id)
    expect(a).toEqual(b)
  })

  it('breaks a full tie (equal title AND key) by id, ascending, regardless of dir', () => {
    // The ONLY way to reach `|| a.id.localeCompare(b.id)`: two series with the SAME
    // title AND the same sort-key value, so both the key compare and the title
    // compare return 0. Input is fed in REVERSE id order ([z2, z1]) with NO shuffle:
    // V8's sort is stable, so if the id tiebreak were removed the comparator would
    // return 0 for the pair and the reversed input order would survive → ['z2','z1'],
    // failing this assertion. The id tiebreak is NOT multiplied by the direction
    // sign, so ascending-by-id holds in BOTH directions.
    const zero = { total: 0, downloaded: 0, wanted: 0, failed: 0, unread: 0 }
    const tied: SeriesSummary[] = [
      series({ id: 'z2', title: 'Same Title', chapterCounts: { ...zero } }),
      series({ id: 'z1', title: 'Same Title', chapterCounts: { ...zero } }),
    ]
    expect(sortSeries(tied, 'unread', 'desc').map((s) => s.id)).toEqual(['z1', 'z2'])
    expect(sortSeries(tied, 'unread', 'asc').map((s) => s.id)).toEqual(['z1', 'z2'])
  })

  it('sorts nulls LAST in BOTH directions', () => {
    // THE TRAP: a nulls-last ASC comparator reverse()d for DESC puts nulls FIRST in
    // DESC. The null check must live OUTSIDE the direction flip.
    const asc = sortSeries(items, 'updated', 'asc')
    const desc = sortSeries(items, 'updated', 'desc')
    expect(asc.at(-1)!.lastChapterDownloadedAt).toBeNull()
    expect(desc.at(-1)!.lastChapterDownloadedAt).toBeNull()
  })

  it('does not mutate the input array', () => {
    const before = items.map((s) => s.id)
    sortSeries(items, 'title', 'desc')
    expect(items.map((s) => s.id)).toEqual(before)
  })

  it('title orders correctly in both directions', () => {
    expect(sortSeries(items, 'title', 'asc').map((s) => s.id)).toEqual(['a', 'b', 'c', 'd', 'e'])
    expect(sortSeries(items, 'title', 'desc').map((s) => s.id)).toEqual(['e', 'd', 'c', 'b', 'a'])
  })

  it('added (createdAt) orders correctly in both directions', () => {
    // createdAt order: c(2020) < a(2021) < e(2021-12) < b(2022) < d(2023)
    expect(sortSeries(items, 'added', 'asc').map((s) => s.id)).toEqual(['c', 'a', 'e', 'b', 'd'])
    expect(sortSeries(items, 'added', 'desc').map((s) => s.id)).toEqual(['d', 'b', 'e', 'a', 'c'])
  })

  it('updated (lastChapterDownloadedAt) orders correctly, nulls last both ways', () => {
    // non-null updated order: d(2022) < a(2023) < c(2024); b,e are null → last.
    // Null tie broken by title: Bravo < Echo.
    expect(sortSeries(items, 'updated', 'asc').map((s) => s.id)).toEqual(['d', 'a', 'c', 'b', 'e'])
    expect(sortSeries(items, 'updated', 'desc').map((s) => s.id)).toEqual(['c', 'a', 'd', 'b', 'e'])
  })

  it('waiting (latestChapterAt) orders longest-waiting/recently-released, nulls last both ways', () => {
    const w = [
      series({ id: 'p', title: 'Papa', latestChapterAt: '2024-01-01T00:00:00Z' }),
      series({ id: 'q', title: 'Quebec', latestChapterAt: null }),
      series({ id: 'r', title: 'Romeo', latestChapterAt: '2026-01-01T00:00:00Z' }),
      series({ id: 's', title: 'Sierra', latestChapterAt: '2025-01-01T00:00:00Z' }),
    ]
    // asc = LONGEST waiting first (oldest release): p(2024) < s(2025) < r(2026); null last.
    expect(sortSeries(w, 'waiting', 'asc').map((x) => x.id)).toEqual(['p', 's', 'r', 'q'])
    // desc = RECENTLY released first: r(2026) > s(2025) > p(2024); null STILL last (not sign-flipped).
    expect(sortSeries(w, 'waiting', 'desc').map((x) => x.id)).toEqual(['r', 's', 'p', 'q'])
  })

  it('unread orders correctly in both directions', () => {
    // unread: d=7, a=3, b=c=e=0. Zero-tie broken by title: Bravo < Charlie < Echo.
    expect(sortSeries(items, 'unread', 'desc').map((s) => s.id)).toEqual(['d', 'a', 'b', 'c', 'e'])
    expect(sortSeries(items, 'unread', 'asc').map((s) => s.id)).toEqual(['b', 'c', 'e', 'a', 'd'])
  })

  it('total (chapterCounts.total) orders correctly in both directions', () => {
    // total: e=4 < b=8 < a=10 < d=12 < c=20.
    expect(sortSeries(items, 'total', 'asc').map((s) => s.id)).toEqual(['e', 'b', 'a', 'd', 'c'])
    expect(sortSeries(items, 'total', 'desc').map((s) => s.id)).toEqual(['c', 'd', 'a', 'b', 'e'])
  })

  it('random is a stable permutation for a fixed seed', () => {
    // Same seed ⇒ identical order across calls — the load-bearing property: the
    // Random order must NOT reshuffle when an unrelated input (search/filter)
    // changes and the kernel re-runs.
    const s1 = sortSeries(items, 'random', 'asc', 42).map((s) => s.id)
    const s2 = sortSeries(items, 'random', 'asc', 42).map((s) => s.id)
    expect(s1).toEqual(s2)
    // It is a permutation of the input (nothing dropped or duplicated).
    expect([...s1].sort()).toEqual(['a', 'b', 'c', 'd', 'e'])
  })
})
