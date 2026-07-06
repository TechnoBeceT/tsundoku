import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, userEvent, within } from 'storybook/test'
import MatchPanel from './MatchPanel.vue'
import { candKey } from '../screens/import.types'
import { scanlatorBreakdown, searchResults } from '../../fixtures/import'

/**
 * Stories for MatchPanel — the Scan Library "Match a source" sub-panel.
 * Rebuilt for Slice P onto the shared `useSourceConfigure` Configure powers
 * (multi-select tray, per-scanlator coverage, importance ranking) — mirrors
 * `SeriesDetail/MatchSourceDialog`'s own stories, minus the dialog chrome
 * (this panel renders inline, and its title is a fixed read-only display, not
 * an editable field). Reuses the Import/Adopt flow's own fixtures
 * (`searchResults`/`scanlatorBreakdown`) since the match endpoint returns the
 * identical `SearchGroup`/`SearchCandidate`/`ScanlatorCoverage` DTO. Flip the
 * Storybook theme toolbar to confirm both dark and light.
 */
const firstCandidateKey = candKey(searchResults[0]!.candidates[0]!)

const meta = {
  title: 'ScanLibrary/MatchPanel',
  component: MatchPanel,
  parameters: { layout: 'padded' },
  args: {
    title: 'Solo Leveling',
    groups: searchResults,
    breakdowns: {},
    searching: false,
    searchError: '',
    busy: false,
    error: '',
  },
} satisfies Meta<typeof MatchPanel>

export default meta
type Story = StoryObj<typeof meta>

/**
 * Groups stage — every cross-source match for this title, tray-enabled
 * (Slice P widened this surface to multi-select — the owner can gather
 * several groups' candidates before configuring, or one-tap pick a single
 * group straight into Configure).
 */
export const GroupsStage: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('2 possible matches · choose one or gather several')).toBeInTheDocument()
    await expect(canvas.getByText('Solo Leveling')).toBeInTheDocument()
    // tray-enabled: every group card renders the "+ Add" toggle.
    await expect(canvas.getAllByText('+ Add').length).toBe(searchResults.length)
  },
}

/**
 * Configure stage — multi-select (every candidate starts selected), one
 * candidate's coverage auto-split across two scanlators (via `breakdowns`),
 * and importance ranking (arrows re-order the selected set). The play
 * function picks the first group to advance from Groups.
 */
export const ConfigureMulti: Story = {
  args: {
    breakdowns: { [firstCandidateKey]: scanlatorBreakdown },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))

    const firstCandidate = searchResults[0]!.candidates[0]!
    await expect(canvas.getByLabelText(`Toggle ${firstCandidate.sourceName}`)).toBeInTheDocument()
    const attach = await canvas.findByRole('button', { name: `Attach ${searchResults[0]!.candidates.length} sources` })
    await expect(attach).toBeEnabled()
  },
}

/** §16 — a match search in flight shows a spinner, not a blank panel. */
export const Searching: Story = {
  args: {
    groups: [],
    searching: true,
  },
}

/** §16 — a failed match search surfaces its own error banner. */
export const SearchFailed: Story = {
  args: {
    groups: [],
    searchError: 'Match search failed — the server returned a 500.',
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText(/Match search failed/)).toBeInTheDocument()
  },
}

/** No cross-source candidates were found for this title at all. */
export const NoMatches: Story = {
  args: {
    groups: [],
  },
}

/** §16 — the confirm (import) mutation itself is in flight: the button spins + disables. */
export const Confirming: Story = {
  args: {
    busy: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
    const attach = await canvas.findByRole('button', { name: `Attach ${searchResults[0]!.candidates.length} sources` })
    await expect(attach).toBeDisabled()
  },
}

/** §16 — the confirm mutation failed: the panel shows the error, selection is preserved. */
export const ConfirmFailed: Story = {
  args: {
    error: 'Import failed — series already exists at that path.',
  },
}
