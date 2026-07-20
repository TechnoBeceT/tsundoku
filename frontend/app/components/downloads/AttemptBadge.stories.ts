import type { Meta, StoryObj } from '@storybook/vue3'
import AttemptBadge from './AttemptBadge.vue'

/**
 * Stories for AttemptBadge — the per-source retry-budget pill "‹source› · N/max".
 * It tints from neutral (fresh) → warn (trying) → danger (exhausted) as the source's
 * budget is spent. Flip the theme toolbar to confirm all three tones read.
 */
const meta = {
  title: 'Downloads/AttemptBadge',
  component: AttemptBadge,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof AttemptBadge>

export default meta
type Story = StoryObj<typeof meta>

/** Fresh — no attempt spent yet (neutral). */
export const Fresh: Story = {
  args: { provider: 'MangaDex', attempts: 0, max: 3 },
}

/** Trying — at least one attempt against the source (amber). */
export const Trying: Story = {
  args: { provider: 'Asura Scans', attempts: 1, max: 5 },
}

/** Exhausted — the source's whole budget is spent (danger). */
export const Exhausted: Story = {
  args: { provider: 'Comix', attempts: 3, max: 3 },
}

/** A long source name ellipsizes so the N/max stays visible. */
export const LongName: Story = {
  args: { provider: 'A Very Long Scanlation Group Name', attempts: 2, max: 4 },
}
