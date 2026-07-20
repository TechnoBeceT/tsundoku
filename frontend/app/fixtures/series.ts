/**
 * Story-only fixtures for the library screens. NOT imported by app code — only
 * by Storybook stories — so the screens stay props-driven and backend-free.
 *
 * Covers: a deterministic mix of real placeholder-image URLs (picsum, seeded by
 * slug) and empty strings, so the LibraryList exercises BOTH the `<img>` path and
 * the branded empty-cover placeholder.
 */
import type { CategorySummary, SeriesSummary } from '../components/screens/types'

/** Seeded placeholder-image URL so each "has cover" card shows a stable image. */
const cover = (slug: string): string => `https://picsum.photos/seed/${slug}/300/420`

/** A healthy, varied page of series — covers + placeholders, paused, completed,
 * fresh (0 downloaded), and some with wanted/failed counts. */
export const seriesPage: SeriesSummary[] = [
  {
    id: '0a4d1c8e-1111-4a00-9000-000000000001',
    title: 'Solo Leveling',
    slug: 'solo-leveling',
    category: 'Manhwa',
    coverUrl: cover('solo-leveling'),
    monitored: true,
    completed: false,
    needsSource: false,
    chapterCounts: { total: 200, downloaded: 120, wanted: 80, failed: 0, unread: 12 },
    createdAt: '2024-01-15T10:00:00Z',
    lastChapterDownloadedAt: '2024-11-20T08:30:00Z',
    latestChapterAt: '2024-11-18T00:00:00Z',
    isStalled: false,
  },
  {
    id: '0a4d1c8e-2222-4a00-9000-000000000002',
    title: 'Berserk',
    slug: 'berserk',
    category: 'Manga',
    coverUrl: cover('berserk'),
    monitored: true,
    completed: false,
    needsSource: false,
    chapterCounts: { total: 376, downloaded: 364, wanted: 10, failed: 2, unread: 3 },
    createdAt: '2023-06-02T12:00:00Z',
    lastChapterDownloadedAt: '2024-09-01T00:00:00Z',
    // Long hiatus — monitored + not completed + no new release in the window ⇒ stalled.
    latestChapterAt: '2024-01-05T00:00:00Z',
    isStalled: true,
  },
  {
    id: '0a4d1c8e-3333-4a00-9000-000000000003',
    title: 'One Piece',
    slug: 'one-piece',
    category: 'Manga',
    coverUrl: cover('one-piece'),
    monitored: true,
    completed: false,
    needsSource: false,
    chapterCounts: { total: 1120, downloaded: 1100, wanted: 18, failed: 2, unread: 0 },
    createdAt: '2022-03-10T09:00:00Z',
    lastChapterDownloadedAt: '2024-12-15T06:00:00Z',
    latestChapterAt: '2024-12-14T00:00:00Z',
    isStalled: false,
  },
  {
    // No cover → branded placeholder; paused (un-monitored) + completed.
    id: '0a4d1c8e-4444-4a00-9000-000000000004',
    title: 'Oyasumi Punpun',
    slug: 'oyasumi-punpun',
    category: 'Manga',
    coverUrl: '',
    monitored: false,
    completed: true,
    needsSource: false,
    chapterCounts: { total: 147, downloaded: 147, wanted: 0, failed: 0, unread: 0 },
    createdAt: '2023-11-01T00:00:00Z',
    lastChapterDownloadedAt: '2023-12-20T00:00:00Z',
    latestChapterAt: '2023-12-18T00:00:00Z',
    isStalled: false,
  },
  {
    // HAS a cover AND needsSource=true — the deliberate cover-independence proof
    // (handover 2026-07-13#15): a metadata cover must never be read as "this
    // series has a live download source".
    id: '0a4d1c8e-5555-4a00-9000-000000000005',
    title: 'The Beginning After The End',
    slug: 'the-beginning-after-the-end',
    category: 'Manhwa',
    coverUrl: cover('tbate'),
    monitored: true,
    completed: false,
    needsSource: true,
    chapterCounts: { total: 195, downloaded: 180, wanted: 14, failed: 1, unread: 5 },
    createdAt: '2024-05-20T14:00:00Z',
    lastChapterDownloadedAt: '2024-10-30T14:00:00Z',
    latestChapterAt: '2024-10-28T00:00:00Z',
    isStalled: false,
  },
  {
    // No cover + freshly adopted: nothing downloaded yet (0% bar, all wanted).
    id: '0a4d1c8e-6666-4a00-9000-000000000006',
    title: 'Omniscient Reader',
    slug: 'omniscient-reader',
    category: 'Manhwa',
    coverUrl: '',
    monitored: true,
    completed: false,
    needsSource: false,
    chapterCounts: { total: 210, downloaded: 0, wanted: 210, failed: 0, unread: 0 },
    createdAt: '2024-12-01T00:00:00Z',
    lastChapterDownloadedAt: null,
    latestChapterAt: null,
    isStalled: false,
  },
  {
    // Paused, partway through, with a long title to exercise the 2-line clamp.
    id: '0a4d1c8e-7777-4a00-9000-000000000007',
    title: 'Tales of Demons and Gods',
    slug: 'tales-of-demons-and-gods',
    category: 'Manhua',
    coverUrl: cover('tdg'),
    monitored: false,
    completed: false,
    needsSource: false,
    chapterCounts: { total: 345, downloaded: 300, wanted: 45, failed: 0, unread: 8 },
    createdAt: '2021-08-14T00:00:00Z',
    lastChapterDownloadedAt: '2024-02-11T00:00:00Z',
    latestChapterAt: '2024-02-10T00:00:00Z',
    isStalled: false,
  },
  {
    id: '0a4d1c8e-8888-4a00-9000-000000000008',
    title: 'Vinland Saga',
    slug: 'vinland-saga',
    category: 'Manga',
    coverUrl: '',
    monitored: true,
    completed: true,
    needsSource: false,
    chapterCounts: { total: 207, downloaded: 207, wanted: 0, failed: 0, unread: 0 },
    createdAt: '2023-02-28T00:00:00Z',
    lastChapterDownloadedAt: null,
    latestChapterAt: null,
    isStalled: false,
  },
]

/** Category filter data — dynamic length, includes empty categories (count 0). */
export const categories: CategorySummary[] = [
  { category: 'Manga', count: 4 },
  { category: 'Manhwa', count: 3 },
  { category: 'Manhua', count: 1 },
  { category: 'Comic', count: 0 },
  { category: 'Other', count: 0 },
]
