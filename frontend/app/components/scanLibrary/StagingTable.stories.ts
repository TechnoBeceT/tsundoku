import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, fn, userEvent, within } from 'storybook/test'
import StagingTable from './StagingTable.vue'
import { pendingEntries, scanEntries } from '../../fixtures/scanLibrary'

/**
 * Stories for StagingTable — the Scan Library review list: status-filter
 * tabs, the staged-entry rows, and the "Load more" pagination affordance.
 * Flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'ScanLibrary/StagingTable',
  component: StagingTable,
  parameters: { layout: 'padded' },
  args: {
    'onSet-status-filter': fn(),
    'onLoad-more': fn(),
    'onImport-disk-only': fn(),
    'onMatch': fn(),
    'onSkip': fn(),
  },
} satisfies Meta<typeof StagingTable>

export default meta
type Story = StoryObj<typeof meta>

/** A mixed set of pending/imported/skipped rows — the default review view. */
export const Default: Story = {
  args: { entries: scanEntries },
}

/** Only pending rows loaded (the "Pending" tab selected). */
export const PendingFilter: Story = {
  args: { entries: pendingEntries, statusFilter: 'pending' },
}

/** No staged entries match the current filter — the empty state shows. */
export const Empty: Story = {
  args: { entries: [] },
}

/** The entries list itself failed to load — a visible, non-dismissible banner. */
export const LoadError: Story = {
  args: { entries: [], entriesError: 'Failed to load staged entries — the server returned a 500.' },
}

/** Initial page still loading — skeleton rows instead of content. */
export const Loading: Story = {
  args: { entries: [], pending: true },
}

/**
 * A full page loaded — "Load more" is visible and actionable, and clicking a
 * filter tab emits `set-status-filter` with the picked status.
 */
export const WithLoadMoreAndTabs: Story = {
  args: { entries: scanEntries, hasMore: true },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement)
    const loadMore = canvas.getByRole('button', { name: 'Load more' })
    await expect(loadMore).toBeInTheDocument()
    await userEvent.click(loadMore)
    await expect(args['onLoad-more']).toHaveBeenCalled()

    const pendingTab = canvas.getByRole('tab', { name: /Pending/ })
    await userEvent.click(pendingTab)
    await expect(args['onSet-status-filter']).toHaveBeenCalledWith('pending')
  },
}

/** A row's busy/error state is forwarded from the table's per-path lookups. */
export const RowBusyAndError: Story = {
  args: {
    entries: scanEntries,
    busyPaths: [scanEntries[0]!.path],
    rowErrors: { [scanEntries[1]!.path]: 'Import failed — series already exists at that path.' },
  },
}
