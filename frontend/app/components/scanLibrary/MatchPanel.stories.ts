import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, fn, userEvent, within } from 'storybook/test'
import MatchPanel from './MatchPanel.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for MatchPanel — the Scan Library "Match a source" sub-panel.
 * Reuses the Import/Adopt flow's own fixture (`searchResults`) since the
 * match endpoint returns the identical `SearchGroup`/`SearchCandidate` DTO.
 * Flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'ScanLibrary/MatchPanel',
  component: MatchPanel,
  parameters: { layout: 'padded' },
  args: {
    title: 'Solo Leveling',
    onConfirm: fn(),
    onBack: fn(),
  },
} satisfies Meta<typeof MatchPanel>

export default meta
type Story = StoryObj<typeof meta>

/**
 * Stage 1 (Groups) — two cross-source matches for this title. Picking one
 * advances into Stage 2 and shows its candidates.
 */
export const GroupsStage: Story = {
  args: {
    groups: searchResults,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByText('2 possible matches · choose one')).toBeInTheDocument()
    await expect(canvas.getByText('Solo Leveling')).toBeInTheDocument()
  },
}

/**
 * Stage 2 (Candidates) — the play function picks the first group, then
 * selects a candidate and confirms; asserts the exact `{source, mangaId,
 * importance}` payload the parent needs to call `importWithMatch`.
 */
export const PickAndConfirm: Story = {
  args: {
    groups: searchResults,
  },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText('Solo Leveling'))

    const firstCandidate = searchResults[0]!.candidates[0]!
    await userEvent.click(await canvas.findByLabelText(`Toggle ${firstCandidate.sourceName}`))

    const confirmButton = await canvas.findByRole('button', { name: 'Confirm match' })
    await expect(confirmButton).toBeEnabled()
    await userEvent.click(confirmButton)

    await expect(args.onConfirm).toHaveBeenCalledWith({
      source: firstCandidate.source,
      mangaId: firstCandidate.mangaId,
      importance: 2,
    })
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

/** §16 — the confirm mutation itself is in flight: the button spins + disables. */
export const Confirming: Story = {
  args: {
    groups: searchResults,
    busy: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText('Solo Leveling'))
    const firstCandidate = searchResults[0]!.candidates[0]!
    await userEvent.click(await canvas.findByLabelText(`Toggle ${firstCandidate.sourceName}`))
    await expect(await canvas.findByRole('button', { name: 'Confirm match' })).toBeDisabled()
  },
}

/** §16 — the confirm mutation failed: the panel shows the error, selection is preserved. */
export const ConfirmFailed: Story = {
  args: {
    groups: searchResults,
    error: 'Import failed — series already exists at that path.',
  },
}
