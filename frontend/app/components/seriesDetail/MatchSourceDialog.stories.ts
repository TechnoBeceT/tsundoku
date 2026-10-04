import { candKey } from '../screens/import.types'
import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
import MatchSourceDialog from './MatchSourceDialog.vue'
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
const firstCandidate = searchResults[0]!.candidates[0]!
const firstCandidateKey = candKey(firstCandidate)

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

/**
 * Configure stage with the async breakdown snapshot (GAP-140) at all three
 * lifecycle states — the first candidate computing, the second ready with its
 * as-of date + refresh control, the third failed with its reason + refresh
 * control. Proves `breakdownSnapshots` reaches the rendered row through the
 * real dialog, not a hand-fed `DisplayRow`.
 */
export const ConfigureCoverageSnapshot: Story = {
  args: {
    breakdowns: {
      [candKey(searchResults[0]!.candidates[0]!)]: [],
      [candKey(searchResults[0]!.candidates[1]!)]: [
        { scanlator: searchResults[0]!.candidates[1]!.sourceName, count: 175, ranges: '1-175' },
      ],
      [candKey(searchResults[0]!.candidates[2]!)]: [],
    },
    breakdownSnapshots: {
      [candKey(searchResults[0]!.candidates[0]!)]: { status: 'pending', computedAt: '', error: '' },
      [candKey(searchResults[0]!.candidates[1]!)]: { status: 'ready', computedAt: new Date(Date.now() - 3 * 24 * 60 * 60 * 1000).toISOString(), error: '' },
      [candKey(searchResults[0]!.candidates[2]!)]: { status: 'failed', computedAt: '', error: 'upstream timed out' },
    },
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

/** Same-source address repair: one candidate and explicit no-redownload copy. */
export const Rematch: Story = {
  args: {
    mode: 'rematch',
    exactSource: searchResults[0]!.candidates[0]!.source,
    sourceLabel: searchResults[0]!.candidates[0]!.sourceName,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}

/** Rematch request in flight: dismissal and duplicate submission are disabled. */
export const RematchSaving: Story = {
  args: {
    mode: 'rematch',
    exactSource: searchResults[0]!.candidates[0]!.source,
    sourceLabel: searchResults[0]!.candidates[0]!.sourceName,
    saving: true,
  },
  play: Rematch.play,
}

export const ProgressiveResults: Story = {
  args: { groups: searchResults, searching: true, pendingSourceCount: 2 },
}
