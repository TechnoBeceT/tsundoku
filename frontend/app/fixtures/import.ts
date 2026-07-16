/**
 * Story-only fixtures for the Import / Adopt flow. NOT imported by app code —
 * only by Storybook stories — so the screen stays props-driven and backend-free.
 *
 * Provides a source list, a couple of cross-source search groups (one with a
 * cover thumbnail, one with an empty thumbnail to exercise the placeholder path),
 * the dynamic category list, and a sample chapter-inspect preview.
 */
import type {
  ChapterInspect,
  ScanlatorCoverage,
  SearchGroup,
  Source,
} from '../components/screens/import.types'

/** Seeded placeholder cover so each "has thumbnail" candidate shows a stable image. */
const cover = (id: number): string => `https://picsum.photos/seed/imp-${id}/120/160`

/** Sources available to search (mirrors `GET /api/sources`). */
export const sources: Source[] = [
  { id: '2499283573021220255', name: 'MangaDex', lang: 'en' },
  { id: '1024627298672457456', name: 'Asura Scans', lang: 'en' },
  { id: '5183633796946525193', name: 'Bato.to', lang: 'en' },
  { id: '3437691801785968169', name: 'Manganato', lang: 'en' },
]

/** The owner's dynamic category list (mirrors `GET /api/categories`). */
export const categories: string[] = ['Manga', 'Manhwa', 'Manhua', 'Comic', 'Other']

/** Grouped search results — two groups, each matched across multiple sources. */
export const searchResults: SearchGroup[] = [
  {
    title: 'Solo Leveling',
    candidates: [
      {
        source: '2499283573021220255',
        sourceName: 'MangaDex',
        lang: 'en',
        mangaId: 1001,
        url: '/manga/1001/solo-leveling',
        realUrl: 'https://source.example/manga/1001/solo-leveling',
        title: 'Solo Leveling',
        thumbnailUrl: cover(1001),
      },
      {
        source: '1024627298672457456',
        sourceName: 'Asura Scans',
        lang: 'en',
        mangaId: 1002,
        url: '/manga/1002/solo-leveling',
        realUrl: 'https://source.example/manga/1002/solo-leveling',
        title: 'Solo Leveling',
        thumbnailUrl: cover(1002),
      },
      {
        source: '3437691801785968169',
        sourceName: 'Manganato',
        lang: 'en',
        mangaId: 1003,
        url: '/manga/1003/solo-leveling',
        realUrl: 'https://source.example/manga/1003/solo-leveling',
        title: 'Solo Leveling',
        thumbnailUrl: '',
      },
    ],
  },
  {
    title: 'Omniscient Reader\'s Viewpoint',
    candidates: [
      {
        source: '2499283573021220255',
        sourceName: 'MangaDex',
        lang: 'en',
        mangaId: 2001,
        url: '/manga/2001/omniscient-readers-viewpoint',
        realUrl: 'https://source.example/manga/2001/omniscient-readers-viewpoint',
        title: 'Omniscient Reader\'s Viewpoint',
        thumbnailUrl: cover(2001),
      },
      {
        source: '5183633796946525193',
        sourceName: 'Bato.to',
        lang: 'en',
        mangaId: 2002,
        url: '/manga/2002/omniscient-reader',
        realUrl: 'https://source.example/manga/2002/omniscient-reader',
        title: 'Omniscient Reader',
        thumbnailUrl: '',
      },
    ],
  },
]

/**
 * A sample per-scanlator chapter-coverage breakdown (what
 * `GET /api/sources/{sourceId}/manga/{mangaId}/breakdown` returns, mapped) —
 * two scanlation groups covering non-overlapping chapter ranges. Reused by
 * the Adopt wizard's auto-split stories and the Series-Detail
 * `MatchDiskProviderDialog` scanlator-pick stories.
 */
export const scanlatorBreakdown: ScanlatorCoverage[] = [
  { scanlator: 'Reset Scans', count: 60, ranges: '1-60' },
  { scanlator: 'Asura Scans', count: 30, ranges: '61-90' },
]

/** A sample chapter-inspect preview (what arrives after `inspect` is emitted). */
export const inspectChapters: ChapterInspect[] = [
  { number: 1, name: 'Prologue' },
  { number: 2, name: 'The Weakest Hunter' },
  { number: 3, name: 'The Double Dungeon' },
  { number: 4, name: 'A Second Chance' },
  { number: 5, name: 'Daily Quest' },
  { number: 6, name: '' },
  { number: 7, name: 'Penalty Zone' },
  { number: null, name: 'Special: Side Story' },
]
