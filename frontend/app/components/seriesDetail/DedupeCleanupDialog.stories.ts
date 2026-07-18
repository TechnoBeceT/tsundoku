import type { Meta, StoryObj } from '@storybook/vue3'
import DedupeCleanupDialog from './DedupeCleanupDialog.vue'
import type { DedupePlanItem } from '../screens/seriesDetail.types'

/**
 * Stories for the dedupe-files preview→confirm dialog. It lists EXACTLY what the
 * "Remove duplicate files" sweep will delete, grouped by reason (engine-switch
 * duplicate rows, ignored fractionals, orphan/duplicate files), so the destructive
 * POST is confirmed against a real list — not run blind. Flip the theme toolbar to
 * check both themes.
 */
const meta = {
  title: 'SeriesDetail/DedupeCleanupDialog',
  component: DedupeCleanupDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof DedupeCleanupDialog>

export default meta
type Story = StoryObj<typeof meta>

/** A plan touching all three removal sources at once. */
const fullPlan: DedupePlanItem[] = [
  { reason: 'epilogue-merge', number: -1, filename: 'Chapter -1 [Toonily] Epilogue Series -1.cbz' },
  { reason: 'ignored-fractional', number: 181.5, filename: '[KaliScan][en] Returner 181.5.cbz' },
  { reason: 'ignored-fractional', number: 190.5, filename: '[KaliScan][en] Returner 190.5.cbz' },
  { reason: 'orphan-superseded', number: 7, filename: '[old][en] Returner 007.cbz' },
  { reason: 'orphan-superseded', number: 9.1, filename: '[stray][en] Returner 009.1.cbz' },
]

/** The full mixed plan — all three groups, confirm reads "Remove 5 items". */
export const Populated: Story = {
  args: { open: true, items: fullPlan },
}

/** Only orphan/duplicate files (the common case for an imported library). */
export const OrphanFilesOnly: Story = {
  args: {
    open: true,
    items: [
      { reason: 'orphan-superseded', number: 10, filename: '[old][en] Sweep 010.cbz' },
      { reason: 'orphan-superseded', number: 10, filename: '[gone][en] Sweep 010.cbz' },
    ],
  },
}

/** Empty plan: the "nothing to remove" state, only Close is offered (no POST fires). */
export const Empty: Story = {
  args: { open: true, items: [] },
}

/** In-flight: the confirm button spins and dismissal is blocked (§16). */
export const Busy: Story = {
  args: { open: true, items: fullPlan, busy: true },
}

/** A FAILED removal: the dialog STAYS open with the reason shown inside it (§16). */
export const WithError: Story = {
  args: { open: true, items: fullPlan, error: 'Dedupe files failed' },
}
