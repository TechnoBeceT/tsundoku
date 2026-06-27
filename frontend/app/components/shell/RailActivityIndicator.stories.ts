import type { Meta, StoryObj } from '@storybook/vue3'
import RailActivityIndicator from './RailActivityIndicator.vue'

/**
 * Stories for the rail-bottom download-activity cluster. Each story frames it on
 * the `var(--rail)` surface (its real home at the foot of the nav rail). Flip the
 * Storybook theme toolbar to confirm the accent + amber tints read in both themes.
 */
const meta = {
  title: 'Shell/RailActivityIndicator',
  component: RailActivityIndicator,
  // Frame on the rail surface, mirroring the rail-foot flex column.
  decorators: [() => ({
    template: '<div style="display:inline-flex;flex-direction:column;align-items:center;gap:10px;padding:14px;border-radius:var(--radius-xl);background:var(--rail)"><story /></div>',
  })],
  argTypes: {
    active: { control: { type: 'number', min: 0 } },
    failed: { control: { type: 'number', min: 0 } },
  },
  args: { active: 2, failed: 0 },
} satisfies Meta<typeof RailActivityIndicator>

export default meta
type Story = StoryObj<typeof meta>

/** Active downloads only — the accent indicator. */
export const Active: Story = {
  args: { active: 2, failed: 0 },
}

/** Failed downloads only — the amber indicator. */
export const Failed: Story = {
  args: { active: 0, failed: 2 },
}

/** Both halves at once. */
export const Both: Story = {
  args: { active: 4, failed: 2 },
}

/** Both counts at 0 — renders nothing (quiet rail). */
export const Empty: Story = {
  args: { active: 0, failed: 0 },
}
