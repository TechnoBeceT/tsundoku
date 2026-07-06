import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
import MatchSourceDialog from './MatchSourceDialog.vue'
import { candKey } from '../screens/import.types'
import { scanlatorBreakdown, searchResults } from '../../fixtures/import'

/**
 * Stories for the Series-Detail "Add a source" dialog. Rebuilt for Slice P
 * onto the shared `useSourceConfigure` Configure powers (multi-select,
 * per-scanlator coverage, importance ranking) — mirrors `Screens/Import`'s
 * Configure-stage stories, minus the title/category fields (this dialog only
 * ADDS sources to an already-existing series). The dialog is presentation-only
 * (open + seriesTitle + groups + breakdowns + §16 state in, search/
 * loadBreakdowns/confirm out), so every state is a pure fixture: the prefilled
 * search box, a no-results search, the multi-select Configure stage (with one
 * candidate's coverage split across two scanlators), a search/attach failure,
 * and the saving (in-flight) state. Flip the theme toolbar for dark/light.
 */
const firstCandidateKey = candKey(searchResults[0]!.candidates[0]!)

const meta = {
  title: 'SeriesDetail/MatchSourceDialog',
  component: MatchSourceDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    seriesTitle: 'Solo Leveling',
    groups: searchResults,
    breakdowns: {},
    searching: false,
    saving: false,
    error: null,
  },
} satisfies Meta<typeof MatchSourceDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Search stage — the box is prefilled with the series' own title. */
export const Search: Story = {}

/** A search that matched nothing (§16 empty state). */
export const NoResults: Story = {
  args: { groups: [] },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: 'Search' }))
  },
}

/**
 * Configure stage — multi-select (every candidate starts selected), one
 * candidate's coverage auto-split across two scanlators (via `breakdowns`),
 * and importance ranking (arrows re-order the selected set). The play
 * function picks the first group to advance from Search.
 */
export const ConfigureMulti: Story = {
  args: {
    breakdowns: { [firstCandidateKey]: scanlatorBreakdown },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}

/** A search or attach failure message banners at the top of the dialog. */
export const Error: Story = {
  args: { error: 'Suwayomi was unreachable' },
}

/** §16 — the batch-attach POST is in flight; the confirm button spins + disables. */
export const Saving: Story = {
  args: { saving: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}
