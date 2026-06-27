import type { Meta, StoryObj } from '@storybook/vue3'
import CycleBanner from './CycleBanner.vue'

/**
 * Stories for CycleBanner — the download-cycle status pill. Covers the running
 * (spinner) state, the idle countdown, and the unknown-interval fallback.
 */
const meta = {
  title: 'Downloads/CycleBanner',
  component: CycleBanner,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof CycleBanner>

export default meta
type Story = StoryObj<typeof meta>

/** A cycle is running — spinner + "in progress". */
export const Running: Story = {
  args: { cycleActive: true },
}

/** Idle with a known next-cycle countdown. */
export const Countdown: Story = {
  args: { cycleActive: false, nextCycleMinutes: 14 },
}

/** Idle with no known interval — the plain "Idle" line. */
export const Idle: Story = {
  args: { cycleActive: false, nextCycleMinutes: null },
}
