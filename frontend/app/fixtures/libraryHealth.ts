/**
 * Story-only fixtures for the Library Health screen. NOT imported by app code —
 * only by the Storybook stories — so the screen stays props-driven and
 * backend-free.
 *
 * The fixture covers several sick series mixing `stale` and `erroring` sources,
 * a source with no `lastSyncedAt` (→ "never synced"), sources that are behind,
 * and inline error messages, so every health badge + meta variant renders.
 */
import type { Provider } from '../components/screens/seriesDetail.types'
import type { SeriesHealth } from '../components/screens/libraryHealth.types'

/** Helper: an ISO timestamp `n` hours in the past (drives the relative labels). */
const hoursAgo = (n: number): string => new Date(Date.now() - n * 3_600_000).toISOString()
/** Helper: an ISO timestamp `n` days in the past. */
const daysAgo = (n: number): string => new Date(Date.now() - n * 86_400_000).toISOString()

// A stale source: synced a while ago, falling behind, no error.
const staleSource = (over: Partial<Provider>): Provider => ({
  id: 'prov-stale',
  provider: 'mangadex',
  scanlator: '',
  language: 'en',
  importance: 30,
  health: 'stale',
  chaptersBehind: 3,
  newestChapterAt: daysAgo(9),
  lastSyncedAt: daysAgo(9),
  lastError: '',
  ...over,
})

// An erroring source: last refresh failed, carries an inline error message.
const erroringSource = (over: Partial<Provider>): Provider => ({
  id: 'prov-err',
  provider: 'flamecomics',
  scanlator: 'Asura Scans',
  language: 'en',
  importance: 20,
  health: 'erroring',
  chaptersBehind: 5,
  newestChapterAt: daysAgo(4),
  lastSyncedAt: hoursAgo(6),
  lastError: 'cloudflare challenge not solved (HTTP 403) after 3 attempts',
  ...over,
})

/** Several sick series with a mix of stale + erroring sources. */
export const sickSeries: SeriesHealth[] = [
  {
    id: 'series-1',
    title: 'Solo Leveling',
    slug: 'solo-leveling',
    sources: [
      erroringSource({ id: 's1-a', provider: 'asurascans', scanlator: 'Asura' }),
      staleSource({ id: 's1-b', provider: 'mangadex', chaptersBehind: 2, lastSyncedAt: daysAgo(11) }),
    ],
  },
  {
    id: 'series-2',
    title: 'The Beginning After The End',
    slug: 'tbate',
    sources: [
      staleSource({
        id: 's2-a',
        provider: 'reaperscans',
        chaptersBehind: 4,
        lastSyncedAt: daysAgo(16),
        newestChapterAt: daysAgo(16),
      }),
    ],
  },
  {
    id: 'series-3',
    title: 'Omniscient Reader',
    slug: 'omniscient-reader',
    sources: [
      erroringSource({
        id: 's3-a',
        provider: 'flamecomics',
        chaptersBehind: 0,
        lastError: 'source returned malformed chapter list (parse error at index 12)',
      }),
      erroringSource({
        id: 's3-b',
        provider: 'mangabuddy',
        scanlator: '',
        language: 'ko',
        chaptersBehind: 7,
        lastSyncedAt: null,
        lastError: 'connection timed out after 30s',
      }),
    ],
  },
  {
    id: 'series-4',
    title: 'Chainsaw Man',
    slug: 'chainsaw-man',
    sources: [
      staleSource({
        id: 's4-a',
        provider: 'mangaplus',
        language: 'ja',
        chaptersBehind: 1,
        lastSyncedAt: daysAgo(8),
      }),
    ],
  },
]
