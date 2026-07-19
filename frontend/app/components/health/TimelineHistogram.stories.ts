import type { Meta, StoryObj } from '@storybook/vue3'
import TimelineHistogram from './TimelineHistogram.vue'
import { timelineBuckets } from '../../fixtures/sourceReport'

/**
 * Stories for the report's signature view — the stacked success/fail timeline.
 * Hover a bar for its exact counts + time. Flip the theme toolbar to confirm the
 * emerald/rose reads in both.
 */
const meta = {
  title: 'Health/TimelineHistogram',
  component: TimelineHistogram,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof TimelineHistogram>

export default meta
type Story = StoryObj<typeof meta>

/**
 * The failure cliff — mostly-green history with a wall of red at the right edge:
 * "failing since ~2h ago" made visual.
 */
export const FailureCliff: Story = {
  args: { buckets: timelineBuckets, bucketSize: 'hour' },
}

/** A healthy source — all green. */
export const Healthy: Story = {
  args: {
    bucketSize: 'hour',
    buckets: timelineBuckets.map((b, i) => ({ ...b, success: 6 + (i % 5), failed: 0, total: 6 + (i % 5) })),
  },
}

/** Empty — no activity in the window. */
export const Empty: Story = {
  args: { buckets: [] },
}
