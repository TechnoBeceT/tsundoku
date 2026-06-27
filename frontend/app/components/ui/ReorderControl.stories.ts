import type { Meta, StoryObj } from '@storybook/vue3'
import ReorderControl from './ReorderControl.vue'

/**
 * Stories for the ReorderControl arrow stepper. The three list positions (top /
 * middle / bottom) show which arrows disable; further stories cover the rank
 * highlight, the no-rank variant, and the fully-disabled state. Flip the theme
 * toolbar to confirm the arrows + rank chip read in both themes.
 */
const meta = {
  title: 'UI/ReorderControl',
  component: ReorderControl,
  argTypes: {
    rank: { control: { type: 'number' } },
    canUp: { control: 'boolean' },
    canDown: { control: 'boolean' },
    topHighlighted: { control: 'boolean' },
    disabled: { control: 'boolean' },
  },
  args: { canUp: true, canDown: true, rank: 2, topHighlighted: false, disabled: false },
} satisfies Meta<typeof ReorderControl>

export default meta
type Story = StoryObj<typeof meta>

/** Top of the list — up disabled, rank-1 highlighted. */
export const Top: Story = {
  args: { canUp: false, canDown: true, rank: 1, topHighlighted: true },
}

/** Middle of the list — both arrows enabled. */
export const Middle: Story = {
  args: { canUp: true, canDown: true, rank: 2 },
}

/** Bottom of the list — down disabled. */
export const Bottom: Story = {
  args: { canUp: true, canDown: false, rank: 3 },
}

/** No rank number — just the arrows. */
export const NoRank: Story = {
  args: { canUp: true, canDown: true, rank: undefined },
}

/** Disabled (e.g. a reorder is in flight). */
export const Disabled: Story = {
  args: { canUp: true, canDown: true, rank: 2, disabled: true },
}
