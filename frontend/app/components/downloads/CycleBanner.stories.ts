import type { Meta, StoryObj } from '@storybook/vue3'
import CycleBanner from './CycleBanner.vue'

/**
 * Stories for CycleBanner — the download-cycle status pill. One story per state the
 * schedule contract can express (running · overrunning · starting · waiting ·
 * unscheduled · unavailable), plus the honest deferred-queue summary that replaces
 * the misleading idle line.
 */
const meta = {
  title: 'Downloads/CycleBanner',
  component: CycleBanner,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof CycleBanner>

export default meta
type Story = StoryObj<typeof meta>

/** A cycle is running on schedule — spinner + "in progress". */
export const Running: Story = {
  args: { cycle: { state: 'running', remainingMs: null } },
}

/**
 * OVERRUNNING — the cycle has already passed its own next-due instant, so the next
 * one starts the moment it returns: back-to-back cycles with zero idle time. Amber,
 * and stated rather than hidden behind a 0:00 countdown.
 */
export const Overrunning: Story = {
  args: { cycle: { state: 'overrunning', remainingMs: null } },
}

/** Between cycles with the next already due — it is starting right now. */
export const Starting: Story = {
  args: { cycle: { state: 'starting', remainingMs: null } },
}

/** Idle with a real countdown to the next cycle. */
export const Countdown: Story = {
  args: { cycle: { state: 'waiting', remainingMs: 843_000 } },
}

/** The loop is not scheduled at all — never started, or its context was cancelled. */
export const Unscheduled: Story = {
  args: { cycle: { state: 'unscheduled', remainingMs: null } },
}

/** The schedule endpoint could not be read — said plainly, never guessed. */
export const Unavailable: Story = {
  args: { cycle: null },
}

/**
 * The honest DEFERRED-queue summary: the whole queue is waiting on sources that are
 * on cooldown, so the pill shows "N waiting on a source · retry ~Nm" (soonest) with
 * a pause glyph — never a bare countdown that reads as "all is well".
 */
export const Deferred: Story = {
  args: {
    cycle: { state: 'waiting', remainingMs: 843_000 },
    deferralSummary: { count: 7, soonestIso: new Date(Date.now() + 18 * 60_000).toISOString() },
  },
}
