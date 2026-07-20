import type { Meta, StoryObj } from '@storybook/vue3'
import CycleTimers from './CycleTimers.vue'

/**
 * Stories for the dual header countdown pill. Flip the Storybook theme toolbar to
 * confirm both themes read. The countdowns are static here (the live ticking is
 * driven by useCycleTimers in the app).
 */
const meta = {
  title: 'Downloads/CycleTimers',
  component: CycleTimers,
  args: {
    downloadRunning: false,
    refreshRunning: false,
    downloadRemainingMs: 43_000,
    refreshRemainingMs: 6_728_000, // 1:52:08
  },
} satisfies Meta<typeof CycleTimers>

export default meta
type Story = StoryObj<typeof meta>

/** Both timers counting down — the default resting state ("0:43 · 1:52:08"). */
export const CountingDown: Story = {}

/** A download cycle is in flight — the download segment shows the running spinner. */
export const DownloadRunning: Story = {
  args: { downloadRunning: true },
}

/** A refresh sweep is in flight — the refresh segment shows the running spinner. */
export const RefreshRunning: Story = {
  args: { refreshRunning: true },
}

/** First mount, before the schedule is known — both segments read "waiting…". */
export const Waiting: Story = {
  args: { downloadRemainingMs: null, refreshRemainingMs: null },
}

/** A near-fire download countdown next to a multi-hour refresh countdown. */
export const MixedRanges: Story = {
  args: { downloadRemainingMs: 7_000, refreshRemainingMs: 4_530_000 }, // 0:07 · 1:15:30
}
