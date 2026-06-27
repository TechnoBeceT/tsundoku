import type { Meta, StoryObj } from '@storybook/vue3'
import SaveFooter from './SaveFooter.vue'

/**
 * Stories for the §16 SaveFooter. Covers all four states (idle / saving /
 * success / error) plus the not-dirty (disabled) case. Flip the theme to confirm
 * the success + error result colours read in both modes.
 */
const meta = {
  title: 'UI/SaveFooter',
  component: SaveFooter,
  args: { state: { status: 'idle' }, dirty: true, label: 'Save changes' },
  render: (args) => ({
    components: { SaveFooter },
    setup: () => ({ args }),
    template: '<div style="max-width:460px"><SaveFooter v-bind="args" /></div>',
  }),
} satisfies Meta<typeof SaveFooter>

export default meta
type Story = StoryObj<typeof meta>

/** Idle with unsaved changes — the button is enabled. */
export const Idle: Story = {}

/** Nothing to save — the button is disabled. */
export const NotDirty: Story = {
  args: { dirty: false },
}

/** Saving — the button spins. */
export const Saving: Story = {
  args: { state: { status: 'saving' } },
}

/** Success — the saved check shows beside the button. */
export const Success: Story = {
  args: { state: { status: 'success' } },
}

/** Error — the failure message is surfaced beside the button. */
export const ErrorState: Story = {
  args: { state: { status: 'error', error: 'Invalid SOCKS port — must be 1–65535.' } },
}
