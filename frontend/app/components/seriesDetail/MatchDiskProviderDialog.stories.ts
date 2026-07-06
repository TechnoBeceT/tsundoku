import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
import MatchDiskProviderDialog from './MatchDiskProviderDialog.vue'
import { searchResults, scanlatorBreakdown } from '../../fixtures/import'

/**
 * Stories for the Series-Detail "Match to source" dialog — the no-re-download
 * Match: attribute an unlinked disk-origin group's existing chapters to a
 * real Suwayomi source/scanlator. Presentation-only (open + copy fields +
 * groups/breakdown + §16 state in, search/pickCandidate/confirm out), so
 * every state is a pure fixture driven via play functions walking the
 * in-component search → pick source → pick scanlator flow. Flip the theme
 * toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/MatchDiskProviderDialog',
  component: MatchDiskProviderDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    seriesTitle: 'Solo Leveling',
    providerLabel: 'Unknown (imported)',
    chapterCount: 45,
    defaultImportance: 1,
    groups: searchResults,
    searching: false,
    breakdown: null,
    breakdownLoading: false,
    saving: false,
    error: null,
  },
} satisfies Meta<typeof MatchDiskProviderDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Idle — the search stage, prefilled with the series' own title + the "no re-download" copy. */
export const Idle: Story = {}

/** A search that matched nothing (§16 empty state). */
export const NoResults: Story = {
  args: { groups: [] },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: 'Search' }))
  },
}

/** Pick stage — a group is picked, listing its candidate sources; none chosen yet. */
export const Pick: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}

/** Breakdown loaded — a candidate is chosen and its scanlator coverage is shown for picking. */
export const BreakdownLoaded: Story = {
  args: { breakdown: scanlatorBreakdown },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
    const target = searchResults[0]!.candidates[0]!
    await userEvent.click(await canvas.findByLabelText(`Toggle ${target.sourceName}`))
  },
}

/** The breakdown fetch failed — the "link all chapters" fallback is offered instead of a split. */
export const BreakdownUnavailable: Story = {
  args: { breakdown: null },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
    const target = searchResults[0]!.candidates[0]!
    await userEvent.click(await canvas.findByLabelText(`Toggle ${target.sourceName}`))
  },
}

/** A search or match failure message banners at the top of the dialog. */
export const Error: Story = {
  args: { error: 'Suwayomi was unreachable' },
}

/** §16 — the match POST is in flight; the confirm button spins + disables. */
export const Saving: Story = {
  args: { saving: true, breakdown: scanlatorBreakdown },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
    const target = searchResults[0]!.candidates[0]!
    await userEvent.click(await canvas.findByLabelText(`Toggle ${target.sourceName}`))
    await userEvent.click(await canvas.findByText(scanlatorBreakdown[0]!.scanlator))
  },
}
