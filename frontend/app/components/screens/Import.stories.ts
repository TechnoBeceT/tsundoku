import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import { userEvent, within } from 'storybook/test'
import { INITIAL_VIEWPORTS } from 'storybook/viewport'
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
  // sources is a required prop; the play-driven stories set their own args, so
  // this default only satisfies the CSF3 story typing.
  args: { sources },
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

/**
 * Stage 1 with a gathered cross-search adopt tray: the play function adds the
 * first group so the tray bar appears above the results and every card's
 * affordance flips to the "+ Add"/"✓ Added" toggle (choose→ is disabled while
 * the tray is non-empty — see the cross-search-adopt-tray spec).
 */
export const SearchWithTray: Story = {
  args: {
    sources,
    searchResults,
    searched: true,
    categories,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const addButtons = await canvas.findAllByRole('button', { name: '+ Add' })
    await userEvent.click(addButtons[0]!)
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

/**
 * Real mobile viewport (QCAT-230/231) — Stage 2 (Configure) at an actual
 * phone-width VIEWPORT, the most crowded stage: each candidate row (select +
 * cover + source/coverage + Inspect + rank stepper) must stack instead of
 * crushing the source name into a sliver, the Stepper must not blow out the
 * page width, and the row list scrolls INSIDE the bounded panel while the
 * title/category fields + Back/Review actions stay pinned above/below it —
 * with zero horizontal overflow at any width.
 */
export const MobileViewport: Story = {
  args: {
    sources,
    searchResults,
    searched: true,
    categories,
  },
  parameters: {
    viewport: { options: INITIAL_VIEWPORTS },
  },
  globals: {
    viewport: { value: 'iphone12', isRotated: false },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByText(searchResults[0]!.title))
  },
}
