import type { Meta, StoryObj } from '@storybook/vue3'
import CycleBanner from './CycleBanner.vue'

/**
 * Stories for CycleBanner — the download-cycle status pill. Covers the running
 * (spinner) state, the idle countdown, the unknown-interval fallback, and the
 * honest deferred-queue summary (in place of the misleading "Idle" line).
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

/**
 * The honest DEFERRED-queue summary: the whole queue is waiting on sources that are
 * on cooldown, so the pill shows "N waiting on a source · retry ~Nm" (soonest) with
 * a pause glyph — never the misleading "Idle — waiting for next cycle".
 */
export const Deferred: Story = {
  args: {
    cycleActive: false,
    deferralSummary: { count: 7, soonestIso: new Date(Date.now() + 18 * 60_000).toISOString() },
  },
}
