/**
 * Story-only fixtures for the Downloads screen. NOT imported by app code — only
 * by Storybook stories — so the screen stays props-driven and backend-free.
 *
 * One flat `DownloadItem[]` covers every tab; the screen derives the Active /
 * Failed / Queued views from `state`. Covers: real placeholder images (picsum)
 * + empty covers (→ branded placeholder), both in-flight states, retryable +
 * terminal failures with error categories / retries / next-attempt, and both
 * queued sub-states (wanted + upgrade_available).
 */
import type { DownloadItem } from '../components/screens/downloads.types'

/** Seeded placeholder-image URL so each "has cover" row shows a stable image. */
const cover = (slug: string): string => `https://picsum.photos/seed/${slug}/120/160`

/** A varied cross-library activity set spanning all six surfaced states. */
export const downloadItems: DownloadItem[] = [
  // ---- Active: downloading / upgrading ----
  {
    chapterId: 'c-0001',
    seriesId: '0a4d1c8e-1111-4a00-9000-000000000001',
    seriesTitle: 'Solo Leveling',
    seriesCategory: 'Manhwa',
    coverUrl: cover('solo-leveling'),
    number: 148,
    name: 'Chapter 148',
    state: 'downloading',
    provider: '2528143451863530665',
    providerName: 'Asura Scans',
  },
  {
    chapterId: 'c-0002',
    seriesId: '0a4d1c8e-2222-4a00-9000-000000000002',
    seriesTitle: 'Berserk',
    seriesCategory: 'Manga',
    coverUrl: '',
    number: 365,
    name: 'The Flower of the Stone Castle',
    state: 'upgrading',
    provider: '2499283573021220255',
    providerName: 'MangaDex',
  },
  {
    chapterId: 'c-0003',
    seriesId: '0a4d1c8e-3333-4a00-9000-000000000003',
    seriesTitle: 'The Beginning After The End',
    seriesCategory: 'Manhwa',
    coverUrl: cover('tbate'),
    number: 181,
    name: 'Chapter 181',
    state: 'downloading',
    provider: '4630885490626382823',
    providerName: 'ComicK',
  },

  // ---- Failed: retryable ----
  {
    chapterId: 'c-0010',
    seriesId: '0a4d1c8e-3333-4a00-9000-000000000003',
    seriesTitle: 'Solo Leveling',
    seriesCategory: 'Manhwa',
    coverUrl: cover('solo-leveling'),
    number: 147,
    name: 'Chapter 147',
    state: 'failed',
    provider: '2499283573021220255',
    providerName: 'MangaDex',
    retries: 2,
    nextAttempt: 'in 12m',
    lastError: 'read tcp 10.0.0.4:443: connection reset by peer',
    errorCategory: 'network',
  },
  {
    chapterId: 'c-0011',
    seriesId: '0a4d1c8e-7777-4a00-9000-000000000007',
    seriesTitle: 'Tales of Demons and Gods',
    seriesCategory: 'Manhua',
    coverUrl: cover('tdg'),
    number: 480,
    name: 'Chapter 480',
    state: 'failed',
    provider: '5183473065805179973',
    providerName: 'Reaper Scans',
    retries: 1,
    nextAttempt: 'in 4m',
    lastError: 'Cloudflare challenge failed (403)',
    errorCategory: 'cloudflare',
  },

  // ---- Failed: terminal ----
  {
    chapterId: 'c-0020',
    seriesId: '0a4d1c8e-5555-4a00-9000-000000000005',
    seriesTitle: 'Omniscient Reader',
    seriesCategory: 'Manhwa',
    coverUrl: '',
    number: 96,
    name: 'Chapter 96',
    state: 'permanently_failed',
    provider: '2528143451863530665',
    providerName: 'Asura Scans',
    retries: 5,
    lastError: 'timeout waiting for page list',
    errorCategory: 'timeout',
  },

  // ---- Queued: wanted ----
  {
    chapterId: 'c-0030',
    seriesId: '0a4d1c8e-6666-4a00-9000-000000000006',
    seriesTitle: 'One Piece',
    seriesCategory: 'Manga',
    coverUrl: cover('one-piece'),
    number: 1122,
    name: 'Chapter 1122',
    state: 'wanted',
    provider: '2499283573021220255',
    providerName: 'MangaDex',
  },
  {
    chapterId: 'c-0031',
    seriesId: '0a4d1c8e-6666-4a00-9000-000000000006',
    seriesTitle: 'One Piece',
    seriesCategory: 'Manga',
    coverUrl: cover('one-piece'),
    number: 1123,
    name: 'Chapter 1123',
    state: 'wanted',
    provider: '2499283573021220255',
    providerName: 'MangaDex',
  },

  // ---- Queued: upgrade_available ----
  {
    chapterId: 'c-0032',
    seriesId: '0a4d1c8e-2222-4a00-9000-000000000002',
    seriesTitle: 'Vinland Saga',
    seriesCategory: 'Manga',
    coverUrl: '',
    number: 207,
    name: 'Chapter 207',
    state: 'upgrade_available',
    provider: '4630885490626382823',
    providerName: 'ComicK',
  },
]

/** Only the in-flight rows — for the Active story. */
export const activeItems: DownloadItem[] = downloadItems.filter(
  (i) => i.state === 'downloading' || i.state === 'upgrading',
)

/** Only the failed/terminal rows — for the Failed story. */
export const failedItems: DownloadItem[] = downloadItems.filter(
  (i) => i.state === 'failed' || i.state === 'permanently_failed',
)

/** Only the queued rows — for the Scheduled story. */
export const queuedItems: DownloadItem[] = downloadItems.filter(
  (i) => i.state === 'wanted' || i.state === 'upgrade_available',
)
