import type { Meta, StoryObj } from '@storybook/vue3'
import Fractionals from './Fractionals.vue'
import { fractionalSeries, partlyRemovable, allIgnored } from '../../fixtures/fractionals'
// The count/policy badges use the shared status tokens (danger palette); they are
// globally imported via index.css in the app + Storybook preview, so the cards
// render with the real colours in both themes. Flip the Storybook theme toolbar.

/**
 * Stories for the library-wide Fractionals screen — the "fix fractional chapters
 * in one place" surface. Each card jumps to its series, toggles the whole-series
 * ignore policy, and opens the (page-owned) cleanup dialog. There is deliberately
 * NO bulk "clean all". Flip the theme toolbar to confirm both themes read.
 */
const meta = {
  title: 'Screens/Fractionals',
  component: Fractionals,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Fractionals>

export default meta
type Story = StoryObj<typeof meta>

/** The default list: a fully-ignored series, a partly-removable one, and one with no policy set yet. */
export const Default: Story = {
  args: {
    series: fractionalSeries,
  },
}

/** Only removable-right-now series (every source already ignores fractionals). */
export const AllIgnored: Story = {
  args: {
    series: [allIgnored],
  },
}

/** Initial load — skeleton cards. */
export const Loading: Story = {
  args: {
    series: [],
    loading: true,
  },
}

/** Nothing to manage — the all-clear empty state. */
export const Empty: Story = {
  args: {
    series: [],
  },
}

/** Rescan in flight — the button shows its spinner and is disabled. */
export const Refreshing: Story = {
  args: {
    series: fractionalSeries,
    refreshing: true,
  },
}

/** One card's whole-series ignore toggle is mid-write (dimmed). */
export const TogglingOne: Story = {
  args: {
    series: fractionalSeries,
    busyIds: [partlyRemovable.seriesId],
  },
}
