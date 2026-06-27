import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import { userEvent, within } from 'storybook/test'
import Import from './Import.vue'
import type { ChapterInspect } from './import.types'
import { categories, inspectChapters, searchResults, sources } from '../../fixtures/import'

/**
 * Stories for the Import / Adopt flow (Screen G). Flip the Storybook theme toolbar
 * to confirm the stepper, cards, and accent gradients read correctly in BOTH dark
 * and light. The screen owns its own step state, so the later-stage stories seed
 * props and use a play function to walk the in-component navigation forward:
 * `Search` (Stage 1 results), `SearchEmpty` (post-search empty), `Configure`
 * (Stage 2 candidate list), `Inspecting` (a candidate's chapter preview), and
 * `Adopting` (Stage 3 with the submit in flight).
 */
const meta = {
  title: 'Screens/Import',
  component: Import,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Import>

export default meta
type Story = StoryObj<typeof meta>

/** Stage 1 with grouped results — pick a group to walk into Configure. */
export const Search: Story = {
  args: {
    sources,
    searchResults,
    searched: true,
    categories,
  },
}

/** Stage 1 after a search that matched nothing (§16 empty state). */
export const SearchEmpty: Story = {
  args: {
    sources,
    searchResults: [],
    searched: true,
    categories,
  },
}

/** Stage 2 (Configure) — the play function picks the first group to advance. */
export const Configure: Story = {
  args: {
    sources,
    searchResults,
    searched: true,
    categories,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}

/**
 * Stage 2 with a candidate's chapter preview resolved. A wrapper supplies the
 * `inspectChapters` prop only after `inspect` fires — proving the §16 round-trip
 * (loading → list) rather than hard-coding the loaded state.
 */
export const Inspecting: Story = {
  render: () => ({
    components: { Import },
    setup() {
      const chapters = ref<ChapterInspect[] | null>(null)
      const onInspect = (): void => { chapters.value = inspectChapters }
      return { sources, searchResults, categories, chapters, onInspect }
    },
    template: `
      <Import
        :sources="sources"
        :search-results="searchResults"
        :searched="true"
        :categories="categories"
        :inspect-chapters="chapters"
        @inspect="onInspect"
      />
    `,
  }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
    const inspectButtons = await canvas.findAllByRole('button', { name: 'Inspect' })
    await userEvent.click(inspectButtons[0]!)
  },
}

/** Stage 3 (Adopt) with the submit in flight — the button spins + disables. */
export const Adopting: Story = {
  args: {
    sources,
    searchResults,
    searched: true,
    adopting: true,
    categories,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
    await userEvent.click(await canvas.findByRole('button', { name: 'Review →' }))
  },
}
