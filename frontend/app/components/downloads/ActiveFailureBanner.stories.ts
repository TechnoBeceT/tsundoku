import type { Meta, StoryObj } from '@storybook/vue3'
import ActiveFailureBanner from './ActiveFailureBanner.vue'

/**
 * Stories for ActiveFailureBanner — the Active-tab "not idle, WAITING" banner shown
 * when the Active list is empty but chapters are failing or sources are cooling down.
 * Each half is a link; each hides independently at 0. Flip the theme toolbar to
 * confirm the danger palette reads on both.
 */
const meta = {
  title: 'Downloads/ActiveFailureBanner',
  component: ActiveFailureBanner,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ActiveFailureBanner>

export default meta
type Story = StoryObj<typeof meta>

/** Both halves: chapters failing AND sources cooling down. */
export const Both: Story = {
  args: { failing: 7, coolingDown: 2 },
}

/** Only failures — the cooling-down half is hidden. */
export const OnlyFailing: Story = {
  args: { failing: 3, coolingDown: 0 },
}

/** Only cooling-down sources — the failing half is hidden. */
export const OnlyCooling: Story = {
  args: { failing: 0, coolingDown: 1 },
}
