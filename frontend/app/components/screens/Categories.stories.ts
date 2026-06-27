import type { Meta, StoryObj } from '@storybook/vue3'
import Categories from './Categories.vue'
import { categories } from '../../fixtures/categories'

/**
 * Stories for the Categories overview — the library-distribution dashboard. Flip
 * the Storybook theme toolbar to confirm it reads correctly in BOTH dark and
 * light. `Default` shows a varied distribution (including zero-count cards),
 * `Loading` shows the skeleton grid, and `Empty` shows the branded empty state.
 */
const meta = {
  title: 'Screens/Categories',
  component: Categories,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Categories>

export default meta
type Story = StoryObj<typeof meta>

/** A varied distribution — populated categories plus a couple of empty (0%) ones. */
export const Default: Story = {
  args: {
    categories,
  },
}

/** Skeleton grid while the categories load. */
export const Loading: Story = {
  args: {
    categories: [],
    loading: true,
  },
}

/** The branded empty state shown when no categories are defined. */
export const Empty: Story = {
  args: {
    categories: [],
  },
}
