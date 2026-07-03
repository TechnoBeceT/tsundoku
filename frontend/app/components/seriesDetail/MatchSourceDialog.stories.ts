import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
import MatchSourceDialog from './MatchSourceDialog.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for the Series-Detail "Match source" dialog. The dialog is
 * presentation-only (open + seriesTitle + groups + §16 state in, search/confirm
 * out), so every state is a pure fixture: the prefilled search box, the picked
 * group's candidate list (via a play function walking the in-component
 * search → pick flow), a no-results search, a search/add failure, and the
 * saving (in-flight) state. Flip the theme toolbar for dark/light.
 */
const meta = {
  title: 'SeriesDetail/MatchSourceDialog',
  component: MatchSourceDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    seriesTitle: 'Solo Leveling',
    groups: searchResults,
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

/** Pick stage — the play function picks the first group to advance. */
export const Pick: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}

/** A search or add failure message banners at the top of the dialog. */
export const Error: Story = {
  args: { error: 'Suwayomi was unreachable' },
}

/** §16 — the addProvider POST is in flight; the confirm button spins + disables. */
export const Saving: Story = {
  args: { saving: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}
