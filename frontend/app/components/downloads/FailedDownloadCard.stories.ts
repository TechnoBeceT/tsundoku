import type { Meta, StoryObj } from '@storybook/vue3'
import FailedDownloadCard from './FailedDownloadCard.vue'
import { failedItems } from '../../fixtures/downloads'

/**
 * Stories for FailedDownloadCard — the failed-tab row variant: the shared row
 * plus retry-count + next-attempt, a retry/reset button, and an expandable
 * last-error panel. Covers the retryable vs terminal label, the §16 in-flight
 * retry state, and the expanded error detail.
 */
const meta = {
  title: 'Downloads/FailedDownloadCard',
  component: FailedDownloadCard,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof FailedDownloadCard>

export default meta
type Story = StoryObj<typeof meta>

// A retryable (failed) row with a network error + next attempt.
const retryable = failedItems.find((i) => i.state === 'failed')!
// A terminal (permanently_failed) row — the button reads "Reset".
const terminal = failedItems.find((i) => i.state === 'permanently_failed')!

/** Retryable failure — collapsed, button reads "Retry". */
export const Retryable: Story = {
  args: { item: retryable },
}

/** Terminal failure — the action is "Reset" instead of "Retry". */
export const Terminal: Story = {
  args: { item: terminal },
}

/** Retry in flight — the button spins and reads "Retrying…" (§16). */
export const Retrying: Story = {
  args: { item: retryable, retrying: true },
}

/** Expanded — the last-error detail panel is open. */
export const Expanded: Story = {
  args: { item: retryable, expanded: true },
}
