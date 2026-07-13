/**
 * Story-only fixtures for the Series Detail screen. NOT imported by app code —
 * only by the Storybook stories — so the screen stays props-driven and
 * backend-free.
 *
 * The rich fixture exercises every one of the 7 chapter states and all 3
 * provider-health values (incl. an erroring source with an inline error and a
 * stale source that is behind), so the badge palette renders in full.
 */
import type { Chapter, MetadataCandidate, Provider, SeriesDetail } from '../components/screens/seriesDetail.types'

/** Helper: an ISO timestamp `n` hours in the past (drives the relative labels). */
const hoursAgo = (n: number): string => new Date(Date.now() - n * 3_600_000).toISOString()
/** Helper: an ISO timestamp `n` days in the past. */
const daysAgo = (n: number): string => new Date(Date.now() - n * 86_400_000).toISOString()

/** The category options offered by the recategorize select. */
export const categoryOptions: string[] = ['Manga', 'Manhwa', 'Manhua', 'Comic', 'Other']

// A chapter run that walks through all seven download states, plus an unknown
// (null-number) chapter to prove the "—" placeholders. Reader progress: chapter
// 1 is unread (never opened), chapter 2 is partially read (exercises the resume
// line); every other chapter carries the not-read defaults (progress is only
// ever meaningful once a chapter is downloaded).
const chapters: Chapter[] = [
  {
    id: 'chapter-0001',
    chapterKey: 'ch-0001',
    number: 1,
    name: 'The Weakest Hunter',
    state: 'downloaded',
    filename: '[mangadex][en] Solo Leveling 0001.cbz',
    pageCount: 42,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0002',
    chapterKey: 'ch-0002',
    number: 2,
    name: 'If I Had Been A Little Stronger',
    state: 'downloaded',
    filename: '[mangadex][en] Solo Leveling 0002.cbz',
    pageCount: 38,
    read: false,
    lastReadPage: 15,
    readAt: null,
  },
  {
    id: 'chapter-0003',
    chapterKey: 'ch-0003',
    number: 3,
    name: 'It’s Like a Game',
    state: 'upgrade_available',
    filename: '[flame][en] Solo Leveling 0003.cbz',
    pageCount: 40,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0004',
    chapterKey: 'ch-0004',
    number: 4,
    name: 'A Bigger Reward',
    state: 'upgrading',
    filename: '[flame][en] Solo Leveling 0004.cbz',
    pageCount: 41,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0005',
    chapterKey: 'ch-0005',
    number: 5,
    name: 'You’ve Been Hiding Your Skills',
    state: 'downloading',
    filename: '',
    pageCount: null,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0006',
    chapterKey: 'ch-0006',
    number: 6,
    name: '',
    state: 'wanted',
    filename: '',
    pageCount: null,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0007',
    chapterKey: 'ch-0007',
    number: 7,
    name: 'Level Up',
    state: 'failed',
    filename: '',
    pageCount: null,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0008',
    chapterKey: 'ch-0008',
    number: 8,
    name: 'A Discovery',
    state: 'permanently_failed',
    filename: '',
    pageCount: null,
    read: false,
    lastReadPage: 0,
    readAt: null,
  },
  {
    id: 'chapter-0009',
    chapterKey: 'ch-0009-extra',
    number: null,
    name: '',
    state: 'wanted',
    filename: '',
    pageCount: null,
    read: false,
    lastReadPage: 0,
    readAt: null,
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
    feedCount: 270,
    feedRanges: '1-269',
    hasFeed: true,
    fractionalCount: 0,
    fractionalChapters: [],
    ignoreFractional: false,
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
    feedCount: 91,
    feedRanges: '1-88, 90-92',
    hasFeed: true,
    fractionalCount: 0,
    fractionalChapters: [],
    ignoreFractional: false,
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
    feedCount: 12,
    feedRanges: '1-12',
    hasFeed: true,
    fractionalCount: 0,
    fractionalChapters: [],
    ignoreFractional: false,
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
  feedCount: 0,
  feedRanges: '',
  hasFeed: false,
  fractionalCount: 0,
  fractionalChapters: [],
  ignoreFractional: false,
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
  chapterCounts: { total: 9, downloaded: 2, wanted: 2, failed: 2, unread: 1 },
  createdAt: '2024-01-15T10:00:00Z',
  lastChapterDownloadedAt: '2024-11-20T08:30:00Z',
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
  feedCount: 0,
  feedRanges: '',
  hasFeed: false,
  fractionalCount: 0,
  fractionalChapters: [],
  ignoreFractional: false,
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

/**
 * A fractional RE-UPLOADER: a mirror that republishes every whole chapter N as a
 * lone "N.1" under its own URL. Its evidence is a long SYSTEMATIC run
 * (1.1, 2.1, 3.1 …) — this is the source the owner ticks "Ignore fractional
 * chapters" on.
 */
export const reuploaderProvider: Provider = {
  id: 'prov-6666',
  provider: '12792',
  providerName: 'Comic Asura',
  linked: true,
  mangaId: 777,
  chapterCount: 50,
  feedCount: 311,
  feedRanges: '1-258',
  hasFeed: true,
  fractionalCount: 9,
  fractionalChapters: ['1.1', '2.1', '3.1', '4.1', '5.1', '6.1', '7.1', '8.1', '9.1'],
  ignoreFractional: false,
  scanlator: '',
  language: 'en',
  importance: 40,
  health: 'ok',
  chaptersBehind: 0,
  newestChapterAt: hoursAgo(3),
  lastSyncedAt: hoursAgo(1),
  lastError: '',
}

/* ============================================================================
 * Rich-card fixtures — the Komga-style catalogue metadata (synopsis, credits,
 * genres/tags, external links). These live on the OPTIONAL RichSeriesMeta fields
 * of SeriesDetail, so they only feed RichSeriesCard; every existing fixture and
 * consumer above is untouched.
 * ========================================================================== */

/** Realistic external links: two trackers, an aggregator, and the official page. */
const soloLevelingLinks = [
  { label: 'AniList', url: 'https://anilist.co/manga/105398/Solo-Leveling' },
  { label: 'MangaDex', url: 'https://mangadex.org/title/32d76d19-8a05-4db0-9fc2-e0b0648fe9d0' },
  { label: 'MangaUpdates', url: 'https://www.mangaupdates.com/series/abcd1234/solo-leveling' },
  { label: 'Official', url: 'https://www.webtoons.com/en/action/solo-leveling/list?title_no=2809' },
]

/** A rich series with the full catalogue metadata — the centrepiece fixture. */
export const richSeriesFull: SeriesDetail = {
  ...richSeries,
  id: '0a4d1c8e-4444-4a00-9000-000000000004',
  // Portrait dimensions (2:3) so the cover renders like a real manga cover —
  // CoverImage fills the portrait box with object-fit: cover (no letterboxing).
  coverUrl: 'https://picsum.photos/seed/solo-leveling/400/600',
  description:
    'Ten years ago, after "the Gate" that connected the real world with the ' +
    'monster world opened, some of the ordinary, everyday people received the ' +
    'power to hunt monsters within the Gate. They are known as "Hunters". However, ' +
    'not all Hunters are powerful. Sung Jin-Woo, nicknamed the weakest Hunter of ' +
    'all mankind, is an E-rank Hunter barely able to survive the lowest-rank ' +
    'dungeons — until a hidden double dungeon nearly kills his whole party and ' +
    'awakens a mysterious System that only he can see, one that lets him grow ' +
    'stronger without limit.',
  altTitles: ['나 혼자만 레벨업', 'Na Honjaman Level Up', 'Only I Level Up'],
  status: 'Completed',
  year: 2018,
  genres: ['Action', 'Adventure', 'Fantasy', 'Shounen', 'Supernatural'],
  tags: ['Overpowered MC', 'Dungeons', 'System', 'Monsters', 'Level Up'],
  authors: ['Chugong', 'Dubu (Redice Studio)'],
  links: soloLevelingLinks,
}

/** No cover — exercises the branded placeholder inside the rich card. */
export const richSeriesNoCover: SeriesDetail = {
  ...richSeriesFull,
  id: '0a4d1c8e-5555-4a00-9000-000000000005',
  coverUrl: '',
}

/**
 * Data-poor: only the base summary + one source, NO description/genres/tags/
 * authors/altTitles/links — proves the card degrades gracefully (whole sections
 * drop out, no empty gaps).
 */
export const richSeriesMinimal: SeriesDetail = {
  ...richSeries,
  id: '0a4d1c8e-6666-4a00-9000-000000000006',
  title: 'Untitled Draft',
  // Portrait cover (see richSeriesFull) — the base `richSeries` cover is smaller.
  coverUrl: 'https://picsum.photos/seed/untitled-draft/400/600',
  providers: [providers[0]!],
}

/**
 * Overflow stress: a very long title, many alt-titles, a long synopsis, and a
 * large genre/tag/link set — proves clamping + wrapping hold up.
 */
export const richSeriesLong: SeriesDetail = {
  ...richSeriesFull,
  id: '0a4d1c8e-7777-4a00-9000-000000000007',
  title: 'The Extraordinarily Long Chronicle of the Weakest Hunter Who Somehow Became the Strongest Shadow Monarch',
  altTitles: [
    '나 혼자만 레벨업',
    'Na Honjaman Level Up',
    'Only I Level Up',
    'Solo Leveling: Ragnarök',
    'I Alone Level-Up',
    'Ore Dake Level Up na Ken',
  ],
  description:
    'Ten years ago, after "the Gate" that connected the real world with the ' +
    'monster world opened, ordinary people began to awaken as Hunters. ' +
    'This synopsis is deliberately verbose so the "Read more" clamp has something ' +
    'to hide: it repeats itself, meanders through the lore of ranks and dungeons, ' +
    'lingers on the double-dungeon incident, describes the System interface in ' +
    'needless detail, and generally runs well past four lines in any reasonable ' +
    'column width so the toggle reliably appears in the overflow story. It keeps ' +
    'going, and going, well beyond what any card would ever show at a glance.',
  genres: ['Action', 'Adventure', 'Comedy', 'Drama', 'Fantasy', 'Horror', 'Mystery', 'Psychological', 'Supernatural', 'Thriller'],
  tags: ['Overpowered MC', 'Dungeons', 'System', 'Monsters', 'Level Up', 'Necromancy', 'Guilds', 'Reincarnation', 'Time Skip', 'Anti-Hero'],
  links: [
    ...soloLevelingLinks,
    { label: 'Anime-Planet', url: 'https://www.anime-planet.com/manga/solo-leveling' },
    { label: 'MyAnimeList', url: 'https://myanimelist.net/manga/121496/Solo_Leveling' },
    { label: 'Kitsu', url: 'https://kitsu.io/manga/solo-leveling' },
  ],
}

/* ============================================================================
 * Metadata "Identify" fixtures — search results for the Komf-style match modal.
 * Story-only: they feed MetadataIdentifyModal / MetadataCandidateCard so the
 * design renders against realistic same-title-across-providers variants.
 * ========================================================================== */

/**
 * A realistic Identify search: many near-identical "Dragon Slayer's Regression"
 * variants across AniList / MangaDex / MangaUpdates / MAL — the exact ambiguity
 * the owner disambiguates by cover + provider. Portrait picsum covers (2:3).
 */
export const metadataCandidates: MetadataCandidate[] = [
  { id: 'anilist:1', provider: 'AniList', title: 'Dragon Slayer’s Regression', coverUrl: 'https://picsum.photos/seed/dsr-anilist/400/600', year: 2023 },
  { id: 'mangadex:1', provider: 'MangaDex', title: 'Dragon-Slayer’s Regression', coverUrl: 'https://picsum.photos/seed/dsr-mangadex/400/600', year: 2023 },
  { id: 'mangaupdates:1', provider: 'MangaUpdates', title: 'The Dragon Slayer’s Regression', coverUrl: 'https://picsum.photos/seed/dsr-mangaupdates/400/600', year: 2022 },
  { id: 'anilist:2', provider: 'AniList', title: 'Regression of the Strongest Dragon Slayer', coverUrl: 'https://picsum.photos/seed/dsr-regress/400/600', year: 2024 },
  { id: 'mal:1', provider: 'MAL', title: 'Dragon Slayer’s Regression (Web Novel)', coverUrl: 'https://picsum.photos/seed/dsr-mal/400/600', year: 2021 },
  { id: 'mangadex:2', provider: 'MangaDex', title: 'Dragon Slayer no Kikan', coverUrl: 'https://picsum.photos/seed/dsr-kikan/400/600', year: 2023 },
  { id: 'mangaupdates:2', provider: 'MangaUpdates', title: 'Return of the Dragon Slayer', coverUrl: '', year: 2020 },
  { id: 'anilist:3', provider: 'AniList', title: 'The Weakest Dragon Slayer Levels Up Again After His Regression Through Time', coverUrl: 'https://picsum.photos/seed/dsr-long/400/600', year: 2024 },
]

/**
 * A source with ONE genuine side-chapter (a `5.5` omake). `.5` is by far the most
 * common fractional in a real library — this is the source the owner must NOT
 * tick, and the reason no automatic rule is allowed to guess.
 */
export const omakeProvider: Provider = {
  id: 'prov-7777',
  provider: '36',
  providerName: 'Asura Scans',
  linked: true,
  mangaId: 778,
  chapterCount: 120,
  feedCount: 121,
  feedRanges: '1-120',
  hasFeed: true,
  fractionalCount: 1,
  fractionalChapters: ['5.5'],
  ignoreFractional: false,
  scanlator: '',
  language: 'en',
  importance: 60,
  health: 'ok',
  chaptersBehind: 0,
  newestChapterAt: hoursAgo(6),
  lastSyncedAt: hoursAgo(2),
  lastError: '',
}
