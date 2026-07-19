import type { Meta, StoryObj } from '@storybook/vue3'
import SuccessRateMeter from './SuccessRateMeter.vue'

/**
 * Stories for the success-rate meter. The tint crosses emerald ≥95% → amber ≥80%
 * → rose below, so health reads without the number. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/SuccessRateMeter',
  component: SuccessRateMeter,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SuccessRateMeter>

export default meta
type Story = StoryObj<typeof meta>

/** Healthy — emerald, with counts. */
export const Healthy: Story = {
  args: { rate: 0.9875, success: 632, total: 640 },
}

/** Degraded — amber. */
export const Degraded: Story = {
  args: { rate: 0.83, success: 150, total: 180 },
}

/** Failing — rose. */
export const Failing: Story = {
  args: { rate: 0.4571, success: 96, total: 210 },
}

/** Compact — percentage + bar only. */
export const Compact: Story = {
  args: { rate: 0.9875, compact: true },
}
