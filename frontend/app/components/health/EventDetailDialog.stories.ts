import type { Meta, StoryObj } from '@storybook/vue3'
import EventDetailDialog from './EventDetailDialog.vue'
import { recentErrors, sourceEvents } from '../../fixtures/sourceReport'

/**
 * Stories for the single-event forensic modal. A failed event shows the human
 * diagnosis (title + explanation + suggestions) over the raw error; a success
 * shows the fields + context only. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/EventDetailDialog',
  component: EventDetailDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof EventDetailDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Open on a captcha failure — the diagnosis engine renders its guidance. */
export const DiagnosedFailure: Story = {
  args: { open: true, event: recentErrors[0]! },
}

/** Open on a rate-limit failure — different diagnosis + suggestions. */
export const RateLimited: Story = {
  args: { open: true, event: recentErrors[1]! },
}

/** Open on a success — fields + metadata context, no diagnosis. */
export const Success: Story = {
  args: { open: true, event: sourceEvents[0]! },
}
