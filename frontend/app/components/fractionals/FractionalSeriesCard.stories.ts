import type { Meta, StoryObj } from '@storybook/vue3'
import FractionalSeriesCard from './FractionalSeriesCard.vue'
import { partlyRemovable, allIgnored, policyNotSet } from '../../fixtures/fractionals'

/**
 * Stories for one row of the Fractionals page. The card shows the two counts
 * (total fractionals vs removable-now), the whole-series ignore toggle with its
 * "N of M sources ignoring" caption, and a "Clean files" button that is disabled
 * when nothing is removable yet. Flip the theme toolbar to check both themes.
 */
const meta = {
  title: 'Fractionals/FractionalSeriesCard',
  component: FractionalSeriesCard,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof FractionalSeriesCard>

export default meta
type Story = StoryObj<typeof meta>

/** Partly removable — a live source still carries one fractional (toggle OFF). */
export const PartlyRemovable: Story = {
  args: {
    series: partlyRemovable,
  },
}

/** Every source ignores fractionals — fully removable, toggle ON. */
export const AllIgnored: Story = {
  args: {
    series: allIgnored,
  },
}

/** No policy set — "Clean files" is disabled until at least one fractional is removable. */
export const PolicyNotSet: Story = {
  args: {
    series: policyNotSet,
  },
}

/** The ignore toggle is mid-write — dimmed + blocked. */
export const Busy: Story = {
  args: {
    series: partlyRemovable,
    busy: true,
  },
}
