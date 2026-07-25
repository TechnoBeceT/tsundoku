import type { Meta, StoryObj } from '@storybook/vue3'
import CycleTimers from './CycleTimers.vue'

/**
 * Stories for the dual header countdown pill — one story per state the schedule
 * contract can express, so every one is visually reviewable. Flip the Storybook
 * theme toolbar to confirm both themes read.
 *
 * The values are static here; in the app useCycleTimers keeps them live from
 * GET /api/engine/schedule plus the SSE cycle boundaries.
 */
const meta = {
  title: 'Downloads/CycleTimers',
  component: CycleTimers,
  args: {
    download: { state: 'waiting', remainingMs: 43_000 },
    refresh: { state: 'waiting', remainingMs: 6_728_000 }, // 1:52:08
  },
} satisfies Meta<typeof CycleTimers>

export default meta
type Story = StoryObj<typeof meta>

/** Both loops waiting — the resting state ("0:43 · 1:52:08"). */
export const CountingDown: Story = {}

/** A download cycle is in flight and on schedule — spinner, no countdown. */
export const DownloadRunning: Story = {
  args: { download: { state: 'running', remainingMs: null } },
}

/** A refresh sweep is in flight — the refresh segment shows the running spinner. */
export const RefreshRunning: Story = {
  args: { refresh: { state: 'running', remainingMs: null } },
}

/**
 * OVERRUNNING — the cycle is taking longer than its own period, so the next run is
 * already due and cycles are running back-to-back with no idle gap. Amber tone,
 * and deliberately NO countdown: a pinned 0:00 would read as a stuck clock.
 * This is the normal steady state at a 90s period with ~113s cycles.
 */
export const DownloadOverrunning: Story = {
  args: { download: { state: 'overrunning', remainingMs: null } },
}

/** Between cycles with the next one already due — it starts immediately. */
export const DownloadStarting: Story = {
  args: { download: { state: 'starting', remainingMs: null } },
}

/**
 * UNSCHEDULED — the loop is not scheduled at all (never started, or its context was
 * cancelled). Muted, and worded as its own claim: it is not "the next run is late".
 */
export const Unscheduled: Story = {
  args: {
    download: { state: 'unscheduled', remainingMs: null },
    refresh: { state: 'unscheduled', remainingMs: null },
  },
}

/**
 * UNREACHABLE — the schedule endpoint could not be read, so nothing is known. The
 * pill says so instead of showing a countdown invented on the client.
 */
export const Unavailable: Story = {
  args: { download: null, refresh: null },
}

/** A near-fire download countdown next to a multi-hour refresh countdown. */
export const MixedRanges: Story = {
  args: {
    download: { state: 'waiting', remainingMs: 7_000 },
    refresh: { state: 'waiting', remainingMs: 4_530_000 }, // 0:07 · 1:15:30
  },
}
