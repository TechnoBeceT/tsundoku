import type { Meta, StoryObj } from '@storybook/vue3'
import DeferralNote from './DeferralNote.vue'

/**
 * Stories for DeferralNote — the per-row "waiting on a source · retry ~Nm" pill
 * shown on a queued chapter whose source is on a persisted cooldown. The retry ETA
 * counts down live against the shared clock; the reason rides in the title tooltip.
 * Flip the theme toolbar to confirm the muted, token-only palette reads on both.
 */
const meta = {
  title: 'Downloads/DeferralNote',
  component: DeferralNote,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof DeferralNote>

export default meta
type Story = StoryObj<typeof meta>

/** An upgrade whose TARGET is cooling down after a Cloudflare knock-back (~23m out). */
export const UpgradeTargetCooldown: Story = {
  args: {
    deferredUntil: new Date(Date.now() + 23 * 60_000).toISOString(),
    source: 'Asura Scans',
    reason: 'Cloudflare challenge failed (403)',
  },
}

/** A plain wanted chapter whose PRIMARY source is inside its download backoff (~6m). */
export const PrimarySourceBackoff: Story = {
  args: {
    deferredUntil: new Date(Date.now() + 6 * 60_000).toISOString(),
    source: 'MangaDex',
    reason: 'read tcp 10.0.0.4:443: connection reset by peer',
  },
}

/** Under a minute out — the ETA reads in seconds ("retry ~40s"). */
export const AlmostDue: Story = {
  args: {
    deferredUntil: new Date(Date.now() + 40_000).toISOString(),
    source: 'Comix',
    reason: 'timeout waiting for page list',
  },
}

/** A tripped circuit-breaker: "waiting on ‹source› — cooling down, retry ~Nm". */
export const CoolingDown: Story = {
  args: {
    deferredUntil: new Date(Date.now() + 15 * 60_000).toISOString(),
    source: 'Asura Scans',
    reason: 'rate limited (429)',
    reasonKind: 'cooling_down',
  },
}

/** A per-chapter fetch backoff: "retrying ~Nm" (the source rides in the tooltip). */
export const Backoff: Story = {
  args: {
    deferredUntil: new Date(Date.now() + 4 * 60_000).toISOString(),
    source: 'MangaDex',
    reason: 'read tcp: connection reset by peer',
    reasonKind: 'backoff',
  },
}
