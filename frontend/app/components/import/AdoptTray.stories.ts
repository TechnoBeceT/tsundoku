import type { Meta, StoryObj } from '@storybook/vue3'
import AdoptTray from './AdoptTray.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for the cross-search adopt tray bar (Stage 1). `configure`/`remove`
 * are logged in the Actions panel. `OneSource` covers the smallest non-empty
 * tray; `ManySources` shows candidates gathered across several searches under
 * different titles (the label reads "Selected across searches" regardless of
 * how many distinct queries actually contributed). Flip the Storybook theme
 * toolbar to confirm both themes.
 */
const meta = {
  title: 'Import/AdoptTray',
  component: AdoptTray,
  parameters: {
    layout: 'padded',
    actions: { handles: ['configure', 'remove'] },
  },
  decorators: [() => ({ template: '<div style="max-width:780px"><story /></div>' })],
} satisfies Meta<typeof AdoptTray>

export default meta
type Story = StoryObj<typeof meta>

/** A single gathered source. */
export const OneSource: Story = {
  args: { candidates: [searchResults[0]!.candidates[0]!] },
}

/** Several sources gathered across multiple (differently-titled) searches. */
export const ManySources: Story = {
  args: { candidates: [...searchResults[0]!.candidates, ...searchResults[1]!.candidates] },
}
