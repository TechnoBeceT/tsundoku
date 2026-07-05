import type { Meta, StoryObj } from '@storybook/vue3'
import SearchGroupCard from './SearchGroupCard.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for one cross-source search group (Stage 1). `pick`/`add`/`remove`
 * are logged in the Actions panel. `Default` has a mix of cover + placeholder
 * candidate pills; `TwoSources` is a smaller group. `TrayActiveNotAdded` and
 * `TrayActiveAdded` cover the cross-search-adopt-tray affordance rule: once
 * the tray holds a candidate, the card stops picking and only the "+ Add"/
 * "✓ Added" toggle responds. Flip the Storybook theme toolbar to confirm both
 * themes.
 */
const meta = {
  title: 'Import/SearchGroupCard',
  component: SearchGroupCard,
  parameters: {
    layout: 'padded',
    actions: { handles: ['pick', 'add', 'remove'] },
  },
  decorators: [() => ({ template: '<div style="max-width:780px"><story /></div>' })],
} satisfies Meta<typeof SearchGroupCard>

export default meta
type Story = StoryObj<typeof meta>

/** Tray empty: the whole card still picks straight to Configure, plus a "+ Add". */
export const Default: Story = {
  args: { group: searchResults[0]! },
}

/** A smaller group matched across two sources. */
export const TwoSources: Story = {
  args: { group: searchResults[1]! },
}

/** Tray non-empty, this group not yet gathered — the card no longer picks; only "+ Add" responds. */
export const TrayActiveNotAdded: Story = {
  args: { group: searchResults[0]!, trayActive: true },
}

/** Tray non-empty and every candidate of this group already gathered — "✓ Added" (click removes it). */
export const TrayActiveAdded: Story = {
  args: { group: searchResults[0]!, trayActive: true, added: true },
}
