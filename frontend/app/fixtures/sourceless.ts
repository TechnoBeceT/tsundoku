/**
 * Story-only fixtures for the Sourceless screen + its per-series cleanup dialog.
 * NOT imported by app code — only by Storybook stories — so both stay
 * props-driven and backend-free.
 *
 * `sampleSourcelessPreview` is the per-series dialog's removable set: three
 * downloaded chapters whose former source is gone (`provider: ''` — Rule 1's
 * `ProviderChapter` carried the source, and it was removed out from under the
 * `Chapter` row). Varied `pageCount` (a normal count, a thin one, and `null` for
 * "never recorded") so the dialog's page-count column is exercised honestly.
 *
 * `sampleSourcelessSeries` is the library-wide screen's row list.
 */
import type { SeriesSourceless, SourcelessCleanupPreview } from '../components/screens/sourceless.types'

/** The per-series cleanup dialog's default 3-chapter removable set. */
export const sampleSourcelessPreview: SourcelessCleanupPreview = {
  chapters: [
    { chapterId: 's-067', number: 67, pageCount: 42, provider: '', filename: '[KaliScan][en] Solo Leveling 067.cbz' },
    { chapterId: 's-070', number: 70, pageCount: 38, provider: '', filename: '[KaliScan][en] Solo Leveling 070.cbz' },
    { chapterId: 's-073', number: 73, pageCount: null, provider: '', filename: '[KaliScan][en] Solo Leveling 073.cbz' },
  ],
}

/** The library-wide Sourceless screen's row list. */
export const sampleSourcelessSeries: SeriesSourceless[] = [
  {
    seriesId: '11111111-1111-1111-1111-111111111111',
    title: 'Solo Leveling',
    displayName: 'Solo Leveling',
    category: 'Manhwa',
    coverUrl: '',
    sourcelessCount: 3,
  },
  {
    seriesId: '22222222-2222-2222-2222-222222222222',
    title: 'Omniscient Reader',
    displayName: 'Omniscient Reader',
    category: 'Manhwa',
    coverUrl: '',
    sourcelessCount: 1,
  },
]
