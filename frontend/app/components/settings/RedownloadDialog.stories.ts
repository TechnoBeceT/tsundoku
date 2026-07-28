import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within, expect } from 'storybook/test'
import RedownloadDialog from './RedownloadDialog.vue'

/**
 * Stories for the Settings → Sources bulk re-download card: filter, preview,
 * then a QCAT-222 confirm gate. Flip the Storybook theme toolbar to confirm both
 * dark and light.
 */
const meta = {
  title: 'Settings/RedownloadDialog',
  component: RedownloadDialog,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof RedownloadDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Empty: nothing checked yet, so only the (disabled) "Check" action shows. */
export const Empty: Story = {
  args: {},
}

/**
 * Checked: the server answered with a real remediation-sized set. The amber cost
 * line is the honest part — 231 chapters at the engine's per-source batch is
 * roughly 24 download cycles, and the throttle is NOT raised to speed it up.
 */
export const Previewed: Story = {
  args: { preview: { matched: 231, perCycle: 10, estimatedCycles: 24 } },
}

/** Checking: the preview request is in flight. */
export const Checking: Story = {
  args: { previewBusy: true },
}

/** Nothing matched: the apply action stays disabled — there is nothing to re-queue. */
export const NoMatches: Story = {
  args: { preview: { matched: 0, perCycle: 10, estimatedCycles: 0 } },
}

/**
 * The QCAT-222 gate. The card's own "Re-download N chapters" button NEVER starts
 * the sweep — it only opens this shared destructive ConfirmModal, whose copy
 * states the two things the owner must weigh: nothing is deleted, and it costs
 * roughly N cycles against one source.
 */
export const ConfirmGate: Story = {
  args: { preview: { matched: 231, perCycle: 10, estimatedCycles: 24 } },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.type(await canvas.findByLabelText('Source'), 'Comix')
    await userEvent.click(await canvas.findByRole('button', { name: /Re-download 231 chapters/ }))
    await expect(await canvas.findByText(/Nothing is deleted/)).toBeInTheDocument()
  },
}

/** Applying: the sweep is running — the confirm button spins and dismissal is blocked. */
export const Applying: Story = {
  args: { preview: { matched: 231, perCycle: 10, estimatedCycles: 24 }, applying: true },
}

/** Applied: the §16 success line, stating explicitly that the old files are still there. */
export const Applied: Story = {
  args: { applyMessage: '231 chapters re-queued. Existing files stay on disk until each replacement lands.' },
}

/** Failed: the backend's own message, never swallowed and never generic. */
export const Failed: Story = {
  args: { applyError: 're-download filter needs a source and a cutoff' },
}
