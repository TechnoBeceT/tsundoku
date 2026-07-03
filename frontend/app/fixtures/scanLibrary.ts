/**
 * Story-only fixtures for the Scan Library screen. NOT imported by app code —
 * only by Storybook stories — so the screen stays props-driven and
 * backend-free.
 *
 * Covers every staging status (pending / imported / skipped), a row already
 * matched to a known set of providers, a row with none (disk-only, unknown
 * provenance), and a row already present in the DB (`alreadyInDb`) so the
 * "In library" badge has something to render against.
 */
import type { ScanEntry, ScanState } from '../components/screens/scanLibrary.types'

/** A varied staged-entry set spanning every status + provider/db-match shape. */
export const scanEntries: ScanEntry[] = [
  {
    path: '/data/manga/Manga/Solo Leveling',
    title: 'Solo Leveling',
    category: 'Manga',
    chapterCount: 179,
    providers: ['asura-scans'],
    status: 'pending',
    alreadyInDb: false,
  },
  {
    path: '/data/manga/Manhwa/Omniscient Reader',
    title: 'Omniscient Reader',
    category: 'Manhwa',
    chapterCount: 142,
    providers: [],
    status: 'pending',
    alreadyInDb: false,
  },
  {
    path: '/data/manga/Manga/Berserk',
    title: 'Berserk',
    category: 'Manga',
    chapterCount: 364,
    providers: ['mangadex', 'kaliscan'],
    status: 'pending',
    alreadyInDb: true,
  },
  {
    path: '/data/manga/Manhua/Tales of Demons and Gods',
    title: 'Tales of Demons and Gods',
    category: 'Manhua',
    chapterCount: 480,
    providers: ['reaper-scans'],
    status: 'imported',
    alreadyInDb: true,
  },
  {
    path: '/data/manga/Comic/Some Duplicate Scan',
    title: 'Some Duplicate Scan',
    category: 'Comic',
    chapterCount: 12,
    providers: [],
    status: 'skipped',
    alreadyInDb: false,
  },
]

/** Only the pending rows — for the default Review-stage story. */
export const pendingEntries: ScanEntry[] = scanEntries.filter((e) => e.status === 'pending')

/** Idle — nothing scanned yet (fresh page load, no prior scan). */
export const idleScanState: ScanState = { status: 'idle', processed: 0, total: 0, error: '' }

/** Scanning, total not yet known — the progress bar is indeterminate. */
export const scanningUnknownTotal: ScanState = { status: 'scanning', processed: 3, total: 0, error: '' }

/** Scanning with a known total — the progress bar is determinate. */
export const scanningWithProgress: ScanState = { status: 'scanning', processed: 42, total: 120, error: '' }

/** Done — a completed successful scan. */
export const doneScanState: ScanState = { status: 'done', processed: 120, total: 120, error: '' }

/** Done, but the walk failed/timed out — the error MUST be rendered (§16). */
export const failedScanState: ScanState = {
  status: 'done',
  processed: 58,
  total: 120,
  error: 'Scan timed out after 10 minutes — 58 of an estimated 120 series were found before the walk stopped.',
}
