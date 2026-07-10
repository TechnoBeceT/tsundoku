/**
 * Story-only fixtures for the reader. NOT imported by app code — only by the
 * Storybook stories — so the reader components stay props-driven and backend-free.
 *
 * `readerChapters` is a short downloaded run (small page counts so the strip
 * renders fast in Storybook); `fakePageUrl` stands in for `useReader.pageUrl`,
 * pointing each page at a stable seeded placeholder image.
 */
import type { ReaderChapter } from '../composables/useReader'

/** A short downloaded chapter run for the strip stories. */
export const readerChapters: ReaderChapter[] = [
  { id: 'ch-1', number: 1, name: 'The Weakest Hunter', pageCount: 3, read: true, lastReadPage: 2 },
  { id: 'ch-2', number: 2, name: 'If I Had Been Stronger', pageCount: 3, read: false, lastReadPage: 1 },
  { id: 'ch-3', number: 3, name: 'It’s Like a Game', pageCount: 4, read: false, lastReadPage: 0 },
  { id: 'ch-4', number: 4, name: 'A Bigger Reward', pageCount: 3, read: false, lastReadPage: 0 },
]

/** Stand-in for useReader.pageUrl — a stable seeded placeholder per (chapter, page). */
export const fakePageUrl = (chapterId: string, n: number): string =>
  `https://picsum.photos/seed/${chapterId}-${n}/800/1200`
