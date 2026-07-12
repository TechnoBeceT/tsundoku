import type { Meta, StoryObj } from '@storybook/vue3'
import ResumeFab from './ResumeFab.vue'

/**
 * Stories for the floating "resume reading" button. "Start" is a series
 * nobody has opened yet; "Continue" is the far more common case, once any
 * downloaded chapter shows progress. Flip the theme toolbar to confirm both.
 */
const meta = {
  title: 'SeriesDetail/ResumeFab',
  component: ResumeFab,
  parameters: { layout: 'fullscreen' },
  args: { label: 'Continue', disabled: false },
} satisfies Meta<typeof ResumeFab>

export default meta
type Story = StoryObj<typeof meta>

/** Progress exists — the common case. */
export const Continue: Story = {
  args: { label: 'Continue' },
}

/** Nobody has opened the series yet. */
export const Start: Story = {
  args: { label: 'Start' },
}

/** Disabled — kept for parity with the app's other action controls. */
export const Disabled: Story = {
  args: { label: 'Continue', disabled: true },
}
