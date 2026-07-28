import type { Meta, StoryObj } from '@storybook/vue3'
import DuplicateSeriesCard from './DuplicateSeriesCard.vue'
import { sampleDuplicateSeries } from '../../fixtures/duplicates'

/**
 * Stories for one row of the Cleanup console's Duplicates tab. The card shows the
 * series identity, how many leftover CBZs are removable AND how much disk they
 * hold, and an "Open series" button — the tab is discovery only, so the removal
 * happens on the series page. Flip the theme toolbar to check both themes.
 */
const meta = {
  title: 'Duplicates/DuplicateSeriesCard',
  component: DuplicateSeriesCard,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof DuplicateSeriesCard>

export default meta
type Story = StoryObj<typeof meta>

const first = sampleDuplicateSeries[0]!

/** Default — a heavy series: hundreds of leftovers, most of a gigabyte. */
export const Default: Story = {
  args: {
    row: first,
  },
}

/** The long tail: exactly one leftover file (the label singularises). */
export const SingleFile: Story = {
  args: {
    row: sampleDuplicateSeries[2]!,
  },
}

/** Many small files — a high count that reclaims very little. */
export const ManySmallFiles: Story = {
  args: {
    row: { ...first, fileCount: 304, reclaimableBytes: 3_145_728 },
  },
}
