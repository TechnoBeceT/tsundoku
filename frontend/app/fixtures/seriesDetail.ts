/**
 * Story-only fixtures for the Series Detail screen. NOT imported by app code —
 * only by the Storybook stories — so the screen stays props-driven and
 * backend-free.
 *
 * The rich fixture exercises every one of the 7 chapter states and all 3
 * provider-health values (incl. an erroring source with an inline error and a
 * stale source that is behind), so the badge palette renders in full.
 */
import type { Chapter, Provider, SeriesDetail } from '../components/screens/seriesDetail.types'

/** Helper: an ISO timestamp `n` hours in the past (drives the relative labels). */
const hoursAgo = (n: number): string => new Date(Date.now() - n * 3_600_000).toISOString()
/** Helper: an ISO timestamp `n` days in the past. */
const daysAgo = (n: number): string => new Date(Date.now() - n * 86_400_000).toISOString()

/** The category options offered by the recategorize select. */
export const categoryOptions: string[] = ['Manga', 'Manhwa', 'Manhua', 'Comic', 'Other']

// A chapter run that walks through all seven download states, plus an unknown
// (null-number) chapter to prove the "—" placeholders.
const chapters: Chapter[] = [
  {
    id: 'chapter-0001',
    chapterKey: 'ch-0001',
    number: 1,
    name: 'The Weakest Hunter',
    state: 'downloaded',
    filename: '[mangadex][en] Solo Leveling 0001.cbz',
    pageCount: 42,
  },
  {
    id: 'chapter-0002',
    chapterKey: 'ch-0002',
    number: 2,
    name: 'If I Had Been A Little Stronger',
    state: 'downloaded',
    filename: '[mangadex][en] Solo Leveling 0002.cbz',
    pageCount: 38,
  },
  {
    id: 'chapter-0003',
    chapterKey: 'ch-0003',
    number: 3,
    name: 'It’s Like a Game',
    state: 'upgrade_available',
    filename: '[flame][en] Solo Leveling 0003.cbz',
    pageCount: 40,
  },
  {
    id: 'chapter-0004',
    chapterKey: 'ch-0004',
    number: 4,
    name: 'A Bigger Reward',
    state: 'upgrading',
    filename: '[flame][en] Solo Leveling 0004.cbz',
    pageCount: 41,
  },
  {
    id: 'chapter-0005',
    chapterKey: 'ch-0005',
    number: 5,
    name: 'You’ve Been Hiding Your Skills',
    state: 'downloading',
    filename: '',
    pageCount: null,
  },
  {
    id: 'chapter-0006',
    chapterKey: 'ch-0006',
    number: 6,
    name: '',
    state: 'wanted',
    filename: '',
    pageCount: null,
  },
  {
    id: 'chapter-0007',
    chapterKey: 'ch-0007',
    number: 7,
    name: 'Level Up',
    state: 'failed',
    filename: '',
    pageCount: null,
  },
  {
    id: 'chapter-0008',
    chapterKey: 'ch-0008',
    number: 8,
    name: 'A Discovery',
    state: 'permanently_failed',
    filename: '',
    pageCount: null,
  },
  {
    id: 'chapter-0009',
    chapterKey: 'ch-0009-extra',
    number: null,
    name: '',
    state: 'wanted',
    filename: '',
    pageCount: null,
  },
]

// Three providers across the full health spectrum; importance higher = preferred.
const providers: Provider[] = [
  {
    id: 'prov-1111',
    provider: '2499283573021220255',
    providerName: 'MangaDex',
    linked: true,
    mangaId: 501,
    chapterCount: 8,
    hasFeed: true,
    scanlator: 'Flame Scans',
    language: 'en',
    importance: 30,
    health: 'ok',
    chaptersBehind: 0,
    newestChapterAt: hoursAgo(5),
    lastSyncedAt: hoursAgo(1),
    lastError: '',
  },
  {
    id: 'prov-2222',
    provider: '2528143451863530665',
    providerName: 'Asura Scans',
    linked: true,
    mangaId: 502,
    chapterCount: 6,
    hasFeed: true,
    scanlator: '',
    language: 'en',
    importance: 20,
    health: 'stale',
    chaptersBehind: 3,
    newestChapterAt: daysAgo(40),
    lastSyncedAt: daysAgo(21),
    lastError: '',
  },
  {
    id: 'prov-3333',
    provider: '5183473065805179973',
    providerName: 'Reaper Scans',
    linked: true,
    mangaId: 503,
    chapterCount: 2,
    hasFeed: true,
    scanlator: 'Reaper',
    language: 'ko',
    importance: 10,
    health: 'erroring',
    chaptersBehind: 12,
    newestChapterAt: daysAgo(2),
    lastSyncedAt: hoursAgo(9),
    lastError: 'HTTP 403: Cloudflare challenge failed (FlareSolverr timeout after 60s)',
  },
]

/** An unlinked disk-origin group: imported from disk, no real source attached yet. */
export const unlinkedProvider: Provider = {
  id: 'prov-disk-4444',
  provider: 'disk:kaizoku',
  providerName: 'Unknown (imported)',
  linked: false,
  mangaId: 0,
  chapterCount: 45,
  hasFeed: false,
  scanlator: '',
  language: 'en',
  importance: 1,
  health: 'ok',
  chaptersBehind: 0,
  newestChapterAt: null,
  lastSyncedAt: null,
  lastError: '',
}

/** A rich series: mixed chapter states + 3 providers of varied health. */
export const richSeries: SeriesDetail = {
  id: '0a4d1c8e-1111-4a00-9000-000000000001',
  title: 'Solo Leveling',
  slug: 'solo-leveling',
  category: 'Manhwa',
  coverUrl: 'https://picsum.photos/seed/solo-leveling/300/420',
  monitored: true,
  completed: false,
  chapterCounts: { total: 9, downloaded: 2, wanted: 2, failed: 2 },
  chapters,
  providers,
  metadataProviderId: null,
}

/** Same series with a single provider — exercises the lone-source layout. */
export const singleProviderSeries: SeriesDetail = {
  ...richSeries,
  providers: [providers[0]!],
}

/** No cover URL — exercises the branded placeholder in the header + meta cards. */
export const noCoverSeries: SeriesDetail = {
  ...richSeries,
  id: '0a4d1c8e-9999-4a00-9000-000000000099',
  title: 'Omniscient Reader’s Viewpoint',
  slug: 'omniscient-reader',
  coverUrl: '',
}

/**
 * A library-imported series carrying one unlinked disk-origin group alongside
 * its linked sources — exercises the "Match to source" row action + the
 * unlinked badge/chapter-count in `SourcesPanel`/`ProviderRow`.
 */
export const seriesWithUnlinkedGroup: SeriesDetail = {
  ...richSeries,
  id: '0a4d1c8e-2222-4a00-9000-000000000002',
  providers: [...providers, unlinkedProvider],
}

/**
 * A drifted duplicate: an unlinked disk-origin group whose `providerName` +
 * `scanlator` match a linked, feed-bearing provider (`prov-1111` / MangaDex /
 * Flame Scans) — the same physical source now split across two rows. Exercises
 * `SourcesPanel`'s duplicate banner + "Clean up" + per-row DUPLICATE badge.
 */
export const duplicateProvider: Provider = {
  id: 'prov-disk-5555',
  provider: 'disk:kaizoku-dup',
  providerName: 'MangaDex',
  linked: false,
  mangaId: 0,
  chapterCount: 8,
  hasFeed: false,
  scanlator: 'Flame Scans',
  language: 'en',
  importance: 1,
  health: 'ok',
  chaptersBehind: 0,
  newestChapterAt: null,
  lastSyncedAt: null,
  lastError: '',
}

/** A series with a drifted duplicate source pair (see `duplicateProvider`). */
export const seriesWithDuplicateProviders: SeriesDetail = {
  ...richSeries,
  id: '0a4d1c8e-3333-4a00-9000-000000000003',
  providers: [...providers, duplicateProvider],
}
