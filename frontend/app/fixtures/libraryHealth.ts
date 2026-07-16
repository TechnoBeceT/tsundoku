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
  provider: '2499283573021220255',
  providerName: 'MangaDex',
  mangaId: 42,
  linked: true,
  chapterCount: 40,
  feedCount: 43,
  feedRanges: '1-43',
  hasFeed: true,
  fractionalCount: 0,
  fractionalChapters: [],
  ignoreFractional: false,
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
  provider: '6511650935329388080',
  providerName: 'Flame Comics',
  mangaId: 77,
  linked: true,
  chapterCount: 30,
  feedCount: 35,
  feedRanges: '1-35',
  hasFeed: true,
  fractionalCount: 0,
  fractionalChapters: [],
  ignoreFractional: false,
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

// An unavailable source: its Suwayomi extension was uninstalled, so the engine
// no longer lists its source id. No last_error and no staleness — the source is
// simply GONE (it used to show a misleading "Healthy · supplies 0"). Mirrors the
// real prod "Manga Ball" case.
const unavailableSource = (over: Partial<Provider>): Provider => ({
  id: 'prov-gone',
  provider: '8842194512033120017',
  providerName: 'Manga Ball',
  mangaId: 15,
  linked: true,
  chapterCount: 0,
  feedCount: 0,
  feedRanges: '',
  hasFeed: false,
  fractionalCount: 0,
  fractionalChapters: [],
  ignoreFractional: false,
  scanlator: '',
  language: 'en',
  importance: 10,
  health: 'unavailable',
  chaptersBehind: 0,
  newestChapterAt: null,
  lastSyncedAt: daysAgo(20),
  lastError: '',
  ...over,
})

/** Several sick series with a mix of stale, erroring + unavailable sources. */
export const sickSeries: SeriesHealth[] = [
  {
    id: 'series-1',
    title: 'Solo Leveling',
    slug: 'solo-leveling',
    sources: [
      erroringSource({ id: 's1-a', provider: '2528143451863530665', providerName: 'Asura Scans', scanlator: 'Asura' }),
      staleSource({ id: 's1-b', provider: '2499283573021220255', providerName: 'MangaDex', chaptersBehind: 2, lastSyncedAt: daysAgo(11) }),
    ],
  },
  {
    id: 'series-2',
    title: 'The Beginning After The End',
    slug: 'tbate',
    sources: [
      staleSource({
        id: 's2-a',
        provider: '5183473065805179973',
        providerName: 'Reaper Scans',
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
        provider: '6511650935329388080',
        providerName: 'Flame Comics',
        chaptersBehind: 0,
        lastError: 'source returned malformed chapter list (parse error at index 12)',
      }),
      erroringSource({
        id: 's3-b',
        provider: '7205846017935949201',
        providerName: 'MangaBuddy',
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
        provider: '1998737332432153860',
        providerName: 'MangaPlus',
        language: 'ja',
        chaptersBehind: 1,
        lastSyncedAt: daysAgo(8),
      }),
    ],
  },
  {
    id: 'series-5',
    title: 'The Greatest Estate Developer',
    slug: 'greatest-estate-developer',
    // Flagged solely because its only live source's extension was uninstalled
    // (the backend lists only the sick source — here, the gone one).
    sources: [unavailableSource({ id: 's5-a' })],
  },
]

/** The lone unavailable source (extension uninstalled) — for focused stories. */
export const unavailableSourceRow: Provider = unavailableSource({ id: 'gone-1' })
