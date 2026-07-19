import type { Meta, StoryObj } from '@storybook/vue3'
import CategoryRow from './CategoryRow.vue'
import { settingsCategories } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for a single category row. Flip the Storybook theme toolbar to confirm
 * both dark and light. The current default row hides its set-default + delete
 * actions and carries the DEFAULT pill but STILL shows rename (QCAT-296 — every
 * category is renameable); every non-default row has all three actions.
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

/**
 * The current default category — no set-default/delete; carries the DEFAULT pill.
 * It STILL shows the rename action (QCAT-296: the default is renameable).
 */
export const DefaultCategory: Story = {
  args: { category: settingsCategories[4]! },
}

/**
 * A demoted "Other" (no longer the default) — a fully ordinary category: rename,
 * delete, and set-default are all available. Proves QCAT-296: once "Other" loses
 * the default role it can be renamed and deleted like any other category.
 */
export const DemotedOther: Story = {
  args: {
    category: { id: 'cat-other', name: 'Other', count: 0, isDefault: false },
  },
}

/** §16 busy — the row dims, blocks input, and shows the "Working…" marker. */
export const Busy: Story = {
  args: { category: settingsCategories[1]!, busy: true },
}

/** Inline rename mode — the display swaps for the rename field + Save/Cancel. */
export const Renaming: Story = {
  args: { category: settingsCategories[0]!, renaming: true, renameValue: 'Manga' },
}
