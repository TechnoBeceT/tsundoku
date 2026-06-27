import type { Meta, StoryObj } from '@storybook/vue3'
import SearchGroupCard from './SearchGroupCard.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for one cross-source search group (Stage 1). `pick` is logged in the
 * Actions panel. `Default` has a mix of cover + placeholder candidate pills;
 * `TwoSources` is a smaller group. Flip the Storybook theme toolbar to confirm
 * both themes.
 */
const meta = {
  title: 'Import/SearchGroupCard',
  component: SearchGroupCard,
  parameters: {
    layout: 'padded',
    actions: { handles: ['pick'] },
  },
  decorators: [() => ({ template: '<div style="max-width:780px"><story /></div>' })],
} satisfies Meta<typeof SearchGroupCard>

export default meta
type Story = StoryObj<typeof meta>

/** A group matched across three sources (one with a placeholder cover). */
export const Default: Story = {
  args: { group: searchResults[0]! },
}

/** A smaller group matched across two sources. */
export const TwoSources: Story = {
  args: { group: searchResults[1]! },
}
