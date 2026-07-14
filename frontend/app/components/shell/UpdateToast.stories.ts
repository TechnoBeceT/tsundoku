import type { Meta, StoryObj } from '@storybook/vue3'
import UpdateToast from './UpdateToast.vue'

/**
 * Stories for the service-worker update prompt. Flip the Storybook theme toolbar
 * to confirm both dark and light. Covers the shown and hidden states.
 */
const meta = {
  title: 'Shell/UpdateToast',
  component: UpdateToast,
  parameters: { layout: 'fullscreen' },
  args: { updateAvailable: true },
} satisfies Meta<typeof UpdateToast>

export default meta
type Story = StoryObj<typeof meta>

/** Shown — a new version is waiting (the Reload prompt). */
export const Shown: Story = {}

/** Hidden — no update available (renders nothing). */
export const Hidden: Story = {
  args: { updateAvailable: false },
}
