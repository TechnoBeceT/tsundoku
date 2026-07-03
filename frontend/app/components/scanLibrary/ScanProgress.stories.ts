import type { Meta, StoryObj } from '@storybook/vue3'
import ScanProgress from './ScanProgress.vue'

/**
 * Stories for ScanProgress — the scan wizard's live progress bar. Flip the
 * Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'ScanLibrary/ScanProgress',
  component: ScanProgress,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ScanProgress>

export default meta
type Story = StoryObj<typeof meta>

/** No total known yet — the bar slides indeterminately. */
export const Indeterminate: Story = {
  args: { processed: 0, total: 0 },
}

/** A total is known — the bar fills to the exact percentage. */
export const Determinate: Story = {
  args: { processed: 42, total: 120 },
}

/** The walk finished — the bar is fully filled. */
export const Complete: Story = {
  args: { processed: 120, total: 120 },
}
