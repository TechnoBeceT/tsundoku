import type { Meta, StoryObj } from '@storybook/vue3'
import SearchGroupCard from './SearchGroupCard.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for one cross-source search group (Stage 1). `pick`/`add`/`remove`
 * are logged in the Actions panel.
 *
 * `trayEnabled` gates the whole Add/Added toggle: the Adopt wizard sets it
 * (`Default`/`TwoSources`/`TrayActive*` below); the single-select match
 * surfaces leave it off — `MatchSurface` shows that toggle-free look, a plain
 * pickable card. `TrayActiveNotAdded`/`TrayActiveAdded` cover the affordance
 * rule: once the tray holds a candidate, the card stops picking and only the
 * "+ Add"/"✓ Added" toggle responds. Flip the Storybook theme toolbar to
 * confirm both themes.
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

/** Adopt wizard, tray empty: the whole card still picks straight to Configure, plus a "+ Add". */
export const Default: Story = {
  args: { group: searchResults[0]!, trayEnabled: true },
}

/** A smaller group matched across two sources (Adopt wizard). */
export const TwoSources: Story = {
  args: { group: searchResults[1]!, trayEnabled: true },
}

/** Single-select match surface (MatchPanel / MatchSourceDialog): no tray, so no toggle — just a pickable card. */
export const MatchSurface: Story = {
  args: { group: searchResults[0]! },
}

/** Tray non-empty, this group not yet gathered — the card no longer picks; only "+ Add" responds. */
export const TrayActiveNotAdded: Story = {
  args: { group: searchResults[0]!, trayEnabled: true, trayActive: true },
}

/** Tray non-empty and every candidate of this group already gathered — "✓ Added" (click removes it). */
export const TrayActiveAdded: Story = {
  args: { group: searchResults[0]!, trayEnabled: true, trayActive: true, added: true },
}
