/**
 * Story-only fixtures for the Duplicates tab of the Cleanup console. NOT imported
 * by app code — only by Storybook stories and component tests — so the screen
 * stays props-driven and backend-free.
 *
 * Shaped after a real library sweep: a long tail of series with a single leftover
 * file, and a few with hundreds. The byte figures vary independently of the file
 * counts (many small files can reclaim less than a few large ones), which is
 * exactly why the card shows both.
 */
import type { SeriesDuplicateFiles } from '../components/screens/duplicates.types'

/** The Duplicates tab's row list, most-actionable first. */
export const sampleDuplicateSeries: SeriesDuplicateFiles[] = [
  {
    seriesId: '33333333-3333-3333-3333-333333333333',
    title: 'Olgami',
    displayName: 'Olgami',
    category: 'Manhwa',
    coverUrl: '',
    fileCount: 198,
    reclaimableBytes: 751_619_276,
  },
  {
    seriesId: '44444444-4444-4444-4444-444444444444',
    title: 'The Boy of Death',
    displayName: 'The Boy of Death',
    category: 'Manhwa',
    coverUrl: '',
    fileCount: 59,
    reclaimableBytes: 547_608_330,
  },
  {
    seriesId: '55555555-5555-5555-5555-555555555555',
    title: 'Trace',
    displayName: 'Trace',
    category: 'Manga',
    coverUrl: '',
    fileCount: 1,
    reclaimableBytes: 12_582_912,
  },
]
