import type { Meta, StoryObj } from '@storybook/vue3'
import Sourceless from './Sourceless.vue'
import { sampleSourcelessSeries } from '../../fixtures/sourceless'

/**
 * Stories for the library-wide Sourceless screen — the "clean up sourceless
 * chapters in one place" surface. Each card opens the (page-owned) cleanup
 * dialog via "Review". There is deliberately NO bulk "clean all". Now
 * presentation-only (mirrors Fractionals), so every state is driven by props
 * with no backend involved. Flip the theme toolbar to confirm both themes read.
 */
const meta = {
  title: 'Screens/Sourceless',
  component: Sourceless,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Sourceless>

export default meta
type Story = StoryObj<typeof meta>

/** The default list: two series with downloaded sourceless chapters. */
export const Default: Story = {
  args: {
    series: sampleSourcelessSeries,
  },
}

/** Nothing to review — the all-clear empty state. */
export const Empty: Story = {
  args: {
    series: [],
  },
}

/** Initial load — skeleton cards. */
export const Loading: Story = {
  args: {
    series: [],
    loading: true,
  },
}
