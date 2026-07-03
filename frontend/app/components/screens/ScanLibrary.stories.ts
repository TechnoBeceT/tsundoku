import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, within } from 'storybook/test'
import ScanLibrary from './ScanLibrary.vue'
import {
  doneScanState,
  failedScanState,
  idleScanState,
  scanEntries,
  scanningUnknownTotal,
  scanningWithProgress,
} from '../../fixtures/scanLibrary'

/**
 * Stories for the Scan Library screen — the two-stage wizard (Scan → Review)
 * over the disk-scan staging table. Each story sets a distinct combination of
 * props so every stage (and the §16 error-visibility requirement) renders
 * without a backend. Flip the Storybook theme toolbar to confirm both dark
 * and light.
 */
const meta = {
  title: 'Screens/ScanLibrary',
  component: ScanLibrary,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof ScanLibrary>

export default meta
type Story = StoryObj<typeof meta>

/** Fresh visit — nothing scanned yet, the Scan stage shows the launch button. */
export const Idle: Story = {
  args: {
    scanState: idleScanState,
    entries: [],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('button', { name: 'Start scan' })).toBeInTheDocument()
  },
}

/** A scan is running with a known total — the Review stage's progress bar is determinate. */
export const ScanningWithProgress: Story = {
  args: {
    scanState: scanningWithProgress,
    entries: [],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('progressbar')).toBeInTheDocument()
    await expect(canvas.getByText('42 / 120')).toBeInTheDocument()
  },
}

/** A scan just started — total isn't known yet, so the bar is indeterminate. */
export const ScanningUnknownTotal: Story = {
  args: {
    scanState: scanningUnknownTotal,
    entries: [],
  },
}

/** A completed scan with a populated staging table — the main Review view. */
export const PopulatedTable: Story = {
  args: {
    scanState: doneScanState,
    entries: scanEntries,
    hasMore: false,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('Solo Leveling')).toBeInTheDocument()
    await expect(canvas.getByRole('button', { name: 'Import all remaining · disk-only' })).toBeInTheDocument()
  },
}

/**
 * §16 — a failed/timed-out scan renders its error visibly (never a silently
 * stuck "still scanning" screen), even though entries already exist from the
 * partial walk.
 */
export const ScanFailed: Story = {
  args: {
    scanState: failedScanState,
    entries: scanEntries.slice(0, 2),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText(/Scan timed out/)).toBeInTheDocument()
  },
}

/** The entries list itself failed to load — a distinct error from the scan error. */
export const EntriesLoadError: Story = {
  args: {
    scanState: doneScanState,
    entries: [],
    entriesError: 'Failed to load staged entries — the server returned a 500.',
  },
}

/** A bulk "import all remaining" run just finished with a mix of hits and misses. */
export const BatchResult: Story = {
  args: {
    scanState: doneScanState,
    entries: scanEntries,
    batchResult: { imported: 118, failed: [{ path: '/data/manga/Manga/Broken Entry', message: 'title already exists' }] },
  },
}

/** Load-more affordance: a full page loaded, more may exist server-side. */
export const WithLoadMore: Story = {
  args: {
    scanState: doneScanState,
    entries: scanEntries,
    hasMore: true,
  },
}
