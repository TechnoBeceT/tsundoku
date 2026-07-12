import type { Meta, StoryObj } from '@storybook/vue3'
import Checkbox from './Checkbox.vue'

/**
 * Stories for the square tick box used by multi-select lists (the
 * fractional-cleanup dialog's chapter rows). Distinct from `Toggle`, which is the
 * on/off switch for a single setting. Flip the theme toolbar to check both themes.
 */
const meta = {
  title: 'UI/Checkbox',
  component: Checkbox,
  args: { modelValue: false, ariaLabel: 'Select item' },
} satisfies Meta<typeof Checkbox>

export default meta
type Story = StoryObj<typeof meta>

/** Unticked. */
export const Unchecked: Story = {
  args: { modelValue: false, ariaLabel: 'Select item' },
}

/** Ticked — accent fill + check mark. */
export const Checked: Story = {
  args: { modelValue: true, ariaLabel: 'Select item' },
}

/** Disabled + ticked: dimmed, non-interactive. */
export const Disabled: Story = {
  args: { modelValue: true, disabled: true, ariaLabel: 'Select item' },
}
