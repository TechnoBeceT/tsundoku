import type { Meta, StoryObj } from '@storybook/vue3'
import FractionalCleanupDialog from './FractionalCleanupDialog.vue'
import type { FractionalCleanupChapter } from '../screens/seriesDetail.types'

/**
 * Stories for the fractional-cleanup dialog. The first story is the OWNER'S REAL
 * CASE (live prod, "A Returner's Magic Should Be Special") and is the reason this
 * is a dialog and not a button: 181.5/190.5 are ONE-PAGE notices, while 221.5 and
 * 223.5 are 132/135-page FULL chapters against a 96p typical. The heuristic
 * pre-ticks the four junk files and leaves the two real chapters unticked +
 * flagged — the owner still decides. Flip the theme toolbar to check both themes.
 */
const meta = {
  title: 'SeriesDetail/FractionalCleanupDialog',
  component: FractionalCleanupDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof FractionalCleanupDialog>

export default meta
type Story = StoryObj<typeof meta>

/** The owner's live removable set, in the order the backend returns it. */
const ownersRealCase: FractionalCleanupChapter[] = [
  { chapterId: 'c-1815', number: 181.5, pageCount: 1, provider: 'KaliScan', filename: '[KaliScan][en] Returner 181.5.cbz' },
  { chapterId: 'c-1905', number: 190.5, pageCount: 1, provider: 'KaliScan', filename: '[KaliScan][en] Returner 190.5.cbz' },
  { chapterId: 'c-31', number: 3.1, pageCount: 5, provider: 'Comic Asura', filename: '[Comic Asura][en] Returner 003.1.cbz' },
  { chapterId: 'c-2245', number: 224.5, pageCount: 16, provider: 'KaliScan', filename: '[KaliScan][en] Returner 224.5.cbz' },
  { chapterId: 'c-2215', number: 221.5, pageCount: 132, provider: 'KaliScan', filename: '[KaliScan][en] Returner 221.5.cbz' },
  { chapterId: 'c-2235', number: 223.5, pageCount: 135, provider: 'KaliScan', filename: '[KaliScan][en] Returner 223.5.cbz' },
]

/**
 * THE OWNER'S REAL CASE: four junk files pre-ticked, the two 132/135-page
 * chapters pre-UNTICKED + "⚠ full-size chapter", confirm reads "Remove 4 files".
 */
export const OwnersRealCase: Story = {
  args: { open: true, typicalPageCount: 96, chapters: ownersRealCase },
}

/** All junk (1-5p notices): everything pre-ticked → "Remove 3 files". */
export const AllJunk: Story = {
  args: {
    open: true,
    typicalPageCount: 96,
    chapters: [
      { chapterId: 'j-1', number: 12.5, pageCount: 1, provider: 'KaliScan', filename: '[KaliScan][en] X 012.5.cbz' },
      { chapterId: 'j-2', number: 40.5, pageCount: 2, provider: 'KaliScan', filename: '[KaliScan][en] X 040.5.cbz' },
      { chapterId: 'j-3', number: 77.1, pageCount: 5, provider: 'Comic Asura', filename: '[Comic Asura][en] X 077.1.cbz' },
    ],
  },
}

/** All full-size: nothing pre-ticked, every row flagged, the confirm button DISABLED. */
export const AllFullSize: Story = {
  args: {
    open: true,
    typicalPageCount: 96,
    chapters: [
      { chapterId: 'f-1', number: 221.5, pageCount: 132, provider: 'KaliScan', filename: '[KaliScan][en] X 221.5.cbz' },
      { chapterId: 'f-2', number: 223.5, pageCount: 135, provider: 'KaliScan', filename: '[KaliScan][en] X 223.5.cbz' },
    ],
  },
}

/**
 * No yardstick (`typicalPageCount = 0` — no whole downloaded chapter to measure
 * against): nothing pre-ticked, nothing flagged. With no evidence the machine
 * must not pre-decide; the owner ticks by hand.
 */
export const NoYardstick: Story = {
  args: {
    open: true,
    typicalPageCount: 0,
    chapters: [
      { chapterId: 'n-1', number: 5.5, pageCount: 18, provider: 'KaliScan', filename: '[KaliScan][en] X 005.5.cbz' },
      { chapterId: 'n-2', number: 9.1, pageCount: null, provider: '', filename: '[KaliScan][en] X 009.1.cbz' },
    ],
  },
}

/** In-flight: confirm spins, the boxes are locked, dismissal is blocked (§16). */
export const Busy: Story = {
  args: { open: true, typicalPageCount: 96, chapters: ownersRealCase, busy: true },
}

/** A FAILED removal: the dialog STAYS open with the reason inside it (§16). */
export const WithError: Story = {
  args: { open: true, typicalPageCount: 96, chapters: ownersRealCase, error: 'Update failed' },
}
