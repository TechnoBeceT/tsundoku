import type { Meta, StoryObj } from '@storybook/vue3'
import InstallButton from './InstallButton.vue'

/**
 * Stories for the floating install affordance. Flip the Storybook theme toolbar
 * to confirm both dark and light. Covers the installable (shown) and hidden states.
 */
const meta = {
  title: 'Shell/InstallButton',
  component: InstallButton,
  parameters: { layout: 'fullscreen' },
  args: { installable: true },
} satisfies Meta<typeof InstallButton>

export default meta
type Story = StoryObj<typeof meta>

/** Installable — the browser offered an install prompt (the button shows). */
export const Installable: Story = {}

/** Hidden — no install prompt available (renders nothing). */
export const Hidden: Story = {
  args: { installable: false },
}
