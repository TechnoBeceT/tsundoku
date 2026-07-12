import { describe, it, expect } from 'vitest'
import { findDriftedProviderIds } from './providerDedup'
import type { Provider } from '~/components/screens/seriesDetail.types'

function prov(p: Partial<Provider> & Pick<Provider, 'id'>): Provider {
  return {
    id: p.id,
    provider: p.provider ?? 'x',
    providerName: p.providerName ?? 'Src',
    linked: p.linked ?? true,
    mangaId: p.mangaId ?? 0,
    chapterCount: p.chapterCount ?? 0,
    feedCount: p.feedCount ?? 0,
    feedRanges: p.feedRanges ?? '',
    hasFeed: p.hasFeed ?? true,
    fractionalCount: p.fractionalCount ?? 0,
    fractionalChapters: p.fractionalChapters ?? [],
    ignoreFractional: p.ignoreFractional ?? false,
    scanlator: p.scanlator ?? '',
    language: p.language ?? 'en',
    importance: p.importance ?? 1,
    health: p.health ?? 'ok',
    chaptersBehind: p.chaptersBehind ?? 0,
    newestChapterAt: p.newestChapterAt ?? null,
    lastSyncedAt: p.lastSyncedAt ?? null,
    lastError: p.lastError ?? '',
  }
}

describe('findDriftedProviderIds', () => {
  it('flags an unlinked disk provider whose name+scanlator match a feed-bearing linked twin', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'disk', linked: false, providerName: 'Hive Scans', scanlator: '', chapterCount: 8 }),
      prov({ id: 'live', linked: true, providerName: 'Hive Scans', scanlator: '', chapterCount: 8 }),
    ])
    expect(ids).toEqual(['disk'])
  })

  it('matches case- and whitespace-insensitively', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'disk', linked: false, providerName: ' hive scans ', scanlator: '', chapterCount: 3 }),
      prov({ id: 'live', linked: true, providerName: 'Hive Scans', scanlator: '', chapterCount: 3 }),
    ])
    expect(ids).toEqual(['disk'])
  })

  it('does NOT flag when scanlators differ', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'disk', linked: false, providerName: 'Comix', scanlator: 'Reset Scans', chapterCount: 5 }),
      prov({ id: 'live', linked: true, providerName: 'Comix', scanlator: 'Asura', chapterCount: 5 }),
    ])
    expect(ids).toEqual([])
  })

  it('does NOT flag when the linked twin has an empty feed (hasFeed false) — backend would skip it', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'disk', linked: false, providerName: 'Hive Scans', scanlator: '', chapterCount: 8 }),
      prov({ id: 'live', linked: true, providerName: 'Hive Scans', scanlator: '', chapterCount: 0, hasFeed: false }),
    ])
    expect(ids).toEqual([])
  })

  it('flags when the linked twin has a non-empty feed even though chapterCount is 0 (legacy-drift substate — the bug this fix closes: disk owns the satisfied chapters, live twin already has a feed)', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'disk', linked: false, providerName: 'Hive Scans', scanlator: '', chapterCount: 8 }),
      prov({ id: 'live', linked: true, providerName: 'Hive Scans', scanlator: '', chapterCount: 0, hasFeed: true }),
    ])
    expect(ids).toEqual(['disk'])
  })

  it('returns empty when there are no duplicates', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'a', linked: true, providerName: 'MangaDex', chapterCount: 2 }),
      prov({ id: 'b', linked: true, providerName: 'WebToon', chapterCount: 3 }),
    ])
    expect(ids).toEqual([])
  })

  it('flags multiple drifted disk providers independently', () => {
    const ids = findDriftedProviderIds([
      prov({ id: 'd1', linked: false, providerName: 'A', scanlator: '', chapterCount: 1 }),
      prov({ id: 'l1', linked: true, providerName: 'A', scanlator: '', chapterCount: 1 }),
      prov({ id: 'd2', linked: false, providerName: 'B', scanlator: '', chapterCount: 1 }),
      prov({ id: 'l2', linked: true, providerName: 'B', scanlator: '', chapterCount: 1 }),
    ])
    expect(ids.sort()).toEqual(['d1', 'd2'])
  })
})
