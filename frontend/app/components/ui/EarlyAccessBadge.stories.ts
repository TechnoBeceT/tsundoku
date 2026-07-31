import type { Meta, StoryObj } from '@storybook/vue3'
import EarlyAccessBadge from './EarlyAccessBadge.vue'

/**
 * Stories for EarlyAccessBadge — the "not published free yet" pill a chapter row
 * shows INSTEAD of its red `StatusBadge` when a source is withholding the chapter
 * behind coins (GAP-141). The "free ~Nd" countdown ticks live against the shared
 * clock; the source's own message rides in the title tooltip. Flip the theme
 * toolbar to confirm the calm accent tint reads on both surfaces.
 */
const meta = {
  title: 'UI/EarlyAccessBadge',
  component: EarlyAccessBadge,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof EarlyAccessBadge>

export default meta
type Story = StoryObj<typeof meta>

/** The common case: a freshly released chapter goes free in three days. */
export const ThreeDaysOut: Story = {
  args: {
    until: new Date(Date.now() + 3 * 24 * 3_600_000).toISOString(),
    reason: 'Chapter locked, coins required',
  },
}

/** Nearly free — the countdown drops to hours as the window closes. */
export const HoursOut: Story = {
  args: {
    until: new Date(Date.now() + 5 * 3_600_000).toISOString(),
    reason: 'Premium chapter — subscription required',
  },
}

/** The window has lapsed: the engine re-checks on the next cycle ("free now"). */
export const DueNow: Story = {
  args: {
    until: new Date(Date.now() - 60_000).toISOString(),
    reason: 'Chapter locked, coins required',
  },
}

/** No expiry known — the pill still states WHY, just without a countdown. */
export const NoExpiry: Story = {
  args: { reason: 'Chapter locked, coins required' },
}
