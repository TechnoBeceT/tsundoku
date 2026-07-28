import type { Meta, StoryObj } from '@storybook/vue3'
import Duplicates from './Duplicates.vue'
import { sampleDuplicateSeries } from '../../fixtures/duplicates'

/**
 * Stories for the Duplicates tab of the Cleanup console — the "which series are
 * wasting disk on leftover CBZs" surface. Discovery only: each card opens its
 * series, where the removal button lives, and there is deliberately no bulk
 * action. Presentation-only, so every state is driven by props with no backend
 * involved. Flip the theme toolbar to confirm both themes read.
 */
const meta = {
  title: 'Screens/Duplicates',
  component: Duplicates,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Duplicates>

export default meta
type Story = StoryObj<typeof meta>

/** The default list: three series, most-actionable first. */
export const Default: Story = {
  args: {
    series: sampleDuplicateSeries,
    totalFiles: 258,
    totalBytes: 1_311_810_518,
  },
}

/** Nothing to clean — the all-clear empty state. */
export const Empty: Story = {
  args: {
    series: [],
    totalFiles: 0,
    totalBytes: 0,
  },
}

/** Initial load — skeleton cards. */
export const Loading: Story = {
  args: {
    series: [],
    totalFiles: 0,
    totalBytes: 0,
    loading: true,
  },
}

/** A manual rescan is in flight — the list stays visible, the button spins. */
export const Refreshing: Story = {
  args: {
    series: sampleDuplicateSeries,
    totalFiles: 258,
    totalBytes: 1_311_810_518,
    refreshing: true,
  },
}
