import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, fn, userEvent, within } from 'storybook/test'
import StagingRow from './StagingRow.vue'
import { scanEntries } from '../../fixtures/scanLibrary'

/**
 * Stories for StagingRow — one staged library-scan entry. Flip the Storybook
 * theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'ScanLibrary/StagingRow',
  component: StagingRow,
  parameters: { layout: 'padded' },
  args: {
    'onImport-disk-only': fn(),
    'onMatch': fn(),
    'onSkip': fn(),
  },
} satisfies Meta<typeof StagingRow>

export default meta
type Story = StoryObj<typeof meta>

const pendingNoDbMatch = scanEntries[0]! // pending, no alreadyInDb, one provider
const pendingNoProvider = scanEntries[1]! // pending, no known provider
const pendingAlreadyInDb = scanEntries[2]! // pending, alreadyInDb, two providers
const imported = scanEntries[3]! // imported
const skipped = scanEntries[4]! // skipped

/**
 * A pending row renders Import/Match/Skip. Clicking Skip emits `skip` with
 * the entry's path — the identity the parent dispatches the mutation on.
 */
export const Pending: Story = {
  args: { entry: pendingNoDbMatch },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('button', { name: 'Import' })).toBeInTheDocument()
    await expect(canvas.getByRole('button', { name: 'Match' })).toBeInTheDocument()
    const skipButton = canvas.getByRole('button', { name: 'Skip' })
    await expect(skipButton).toBeInTheDocument()

    await userEvent.click(skipButton)
    await expect(args.onSkip).toHaveBeenCalledWith(pendingNoDbMatch.path)
  },
}

/** A pending row with no recorded provider — the "no known provider" note shows. */
export const PendingNoProvider: Story = {
  args: { entry: pendingNoProvider },
}

/** A pending row already present in the DB shows the "In library" badge. */
export const PendingAlreadyInDb: Story = {
  args: { entry: pendingAlreadyInDb },
}

/**
 * An imported row shows the imported pill and NO Import/Match/Skip buttons —
 * there's nothing left to do with it from this table.
 */
export const Imported: Story = {
  args: { entry: imported },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('Imported')).toBeInTheDocument()
    await expect(canvas.queryByRole('button', { name: 'Import' })).not.toBeInTheDocument()
    await expect(canvas.queryByRole('button', { name: 'Match' })).not.toBeInTheDocument()
    await expect(canvas.queryByRole('button', { name: 'Skip' })).not.toBeInTheDocument()
  },
}

/** A skipped row shows the skipped pill and no actions either. */
export const Skipped: Story = {
  args: { entry: skipped },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('Skipped')).toBeInTheDocument()
    await expect(canvas.queryByRole('button', { name: 'Import' })).not.toBeInTheDocument()
  },
}

/** §16 busy — the row dims and shows a spinner instead of its buttons. */
export const Busy: Story = {
  args: { entry: pendingNoDbMatch, busy: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.queryByRole('button', { name: 'Import' })).not.toBeInTheDocument()
  },
}

/** §16 error — a failed mutation surfaces its own inline banner on the row. */
export const WithError: Story = {
  args: { entry: pendingNoDbMatch, error: 'Import failed — series already exists at that path.' },
}
