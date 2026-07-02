/**
 * Story-only fixtures for the Discover browse screen. NOT imported by app code —
 * only by Storybook stories — so the screen stays props-driven and backend-free.
 *
 * Covers: a deterministic mix of real placeholder-image URLs (picsum, seeded by
 * mangaId) and empty thumbnails, so the grid exercises BOTH the `<img>` path and
 * the initial-letter placeholder. Several candidates carry a description + genres
 * (to fill the hover popup) and one is flagged `inLibrary`. Solo Leveling and
 * Chainsaw Man additionally carry author/artist (M4) — one with distinct credits
 * (exercises the "by X · art by Y" line) and one with a single credit (exercises
 * the "by X" line with no redundant art-by repeat); Berserk stays fully bare to
 * keep exercising the popup's graceful no-metadata-at-all fallback.
 */
import type { BrowseResult, DiscoverCandidate, DiscoverSource } from '../components/screens/discover.types'

/** Seeded placeholder cover so each "has thumbnail" card shows a stable image. */
const cover = (id: number): string => `https://picsum.photos/seed/disc-${id}/300/420`

/** Sources available to browse (mirrors `GET /api/sources`). */
export const sources: DiscoverSource[] = [
  { id: '2499283573021220255', name: 'MangaDex', lang: 'en' },
  { id: '1024627298672457456', name: 'Asura Scans', lang: 'en' },
  { id: '5183633796946525193', name: 'Bato.to', lang: 'en' },
  { id: '3437691801785968169', name: 'Manganato', lang: 'en' },
]

/** Builds a candidate, defaulting source identity to MangaDex. */
const make = (c: Partial<DiscoverCandidate> & Pick<DiscoverCandidate, 'mangaId' | 'title'>): DiscoverCandidate => ({
  source: '2499283573021220255',
  sourceName: 'MangaDex',
  lang: 'en',
  thumbnailUrl: cover(c.mangaId),
  url: `https://mangadex.org/title/${c.mangaId}`,
  ...c,
})

/** A populated Popular page — covers + placeholders, one in-library, rich popups. */
export const popularResult: BrowseResult = {
  page: 1,
  hasNextPage: true,
  manga: [
    make({
      mangaId: 1001,
      title: 'Solo Leveling',
      inLibrary: true,
      description: 'Ten years ago, "the Gate" connected the real world to a realm of monsters. Sung Jinwoo, the weakest of all hunters, is granted a mysterious power to level up in ways no one else can.',
      genres: ['Action', 'Fantasy', 'Adventure'],
      author: 'Chugong',
      artist: 'Dubu (REDICE Studio)',
    }),
    make({
      mangaId: 1002,
      title: 'Chainsaw Man',
      description: 'Denji is a young man trapped in poverty, working off his dead father\'s debt by harvesting devil corpses with his pet devil Pochita.',
      genres: ['Action', 'Horror', 'Comedy'],
      author: 'Tatsuki Fujimoto',
      artist: 'Tatsuki Fujimoto',
    }),
    make({
      mangaId: 1003,
      title: 'The Beginning After The End',
      thumbnailUrl: '',
      description: 'King Grey has unrivaled strength, wealth, and prestige in a world governed by martial ability. Reincarnated into a new world filled with magic and monsters, he gets a second chance.',
      genres: ['Fantasy', 'Isekai', 'Action'],
    }),
    make({
      mangaId: 1004,
      title: 'Jujutsu Kaisen',
      description: 'Yuji Itadori swallows a cursed talisman — the finger of a demon — and becomes host to a powerful curse.',
      genres: ['Action', 'Supernatural'],
    }),
    make({
      mangaId: 1005,
      title: 'Omniscient Reader\'s Viewpoint',
      thumbnailUrl: '',
      description: 'Kim Dokja was an ordinary reader of a web novel — until the story he alone finished becomes reality.',
      genres: ['Action', 'Fantasy', 'Drama'],
    }),
    make({
      mangaId: 1006,
      title: 'Tower of God',
      description: 'Twenty-Fifth Bam has spent most of his life trapped beneath a great Tower, with only his close friend Rachel to keep him company.',
      genres: ['Adventure', 'Fantasy', 'Mystery'],
    }),
    make({
      mangaId: 1007,
      title: 'Berserk',
      // No description/genres → exercises the popup's graceful empty fallback.
    }),
    make({
      mangaId: 1008,
      title: 'Oshi no Ko',
      thumbnailUrl: '',
      description: 'A countryside gynecologist meets his favorite idol when she shows up pregnant at his hospital.',
      genres: ['Drama', 'Supernatural'],
    }),
  ],
}

/** A populated Latest page — a different, last-page slice (no further pages). */
export const latestResult: BrowseResult = {
  page: 1,
  hasNextPage: false,
  manga: [
    make({
      mangaId: 2001,
      title: 'Sakamoto Days',
      description: 'Taro Sakamoto was the ultimate assassin, feared by villains and revered by hitmen. Then he fell in love, got married, and now runs a corner store.',
      genres: ['Action', 'Comedy'],
    }),
    make({
      mangaId: 2002,
      title: 'Kagurabachi',
      thumbnailUrl: '',
      description: 'Chihiro spends his days training under his famed swordsmith father — until tragedy strikes and he sets out for revenge.',
      genres: ['Action', 'Supernatural'],
    }),
    make({
      mangaId: 2003,
      title: 'Blue Lock',
      description: 'After Japan\'s disastrous loss, a radical training program isolates 300 strikers to forge the world\'s greatest egoist.',
      genres: ['Sports', 'Drama'],
    }),
    make({
      mangaId: 2004,
      title: 'Dandadan',
      description: 'An occult-obsessed boy and a ghost-skeptic girl bet on whether aliens or spirits are real — and discover both are.',
      genres: ['Action', 'Comedy', 'Supernatural'],
    }),
  ],
}
