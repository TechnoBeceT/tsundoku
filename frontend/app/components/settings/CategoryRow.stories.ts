import type { Meta, StoryObj } from '@storybook/vue3'
import CategoryRow from './CategoryRow.vue'
import { settingsCategories } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for a single category row. Flip the Storybook theme toolbar to confirm
 * both dark and light. The protected "Other" row hides the edit/delete actions.
 */
const meta = {
  title: 'Settings/CategoryRow',
  component: CategoryRow,
  parameters: { layout: 'padded' },
  args: {
    canUp: true,
    canDown: true,
    busy: false,
    renaming: false,
    renameValue: '',
  },
} satisfies Meta<typeof CategoryRow>

export default meta
type Story = StoryObj<typeof meta>

/** A normal category — rename/delete/set-default actions all available. */
export const Default: Story = {
  args: { category: settingsCategories[0]! },
}

/** The protected default category — no rename/delete; carries the DEFAULT pill. */
export const ProtectedDefault: Story = {
  args: { category: settingsCategories[4]! },
}

/** §16 busy — the row dims, blocks input, and shows the "Working…" marker. */
export const Busy: Story = {
  args: { category: settingsCategories[1]!, busy: true },
}

/** Inline rename mode — the display swaps for the rename field + Save/Cancel. */
export const Renaming: Story = {
  args: { category: settingsCategories[0]!, renaming: true, renameValue: 'Manga' },
}
