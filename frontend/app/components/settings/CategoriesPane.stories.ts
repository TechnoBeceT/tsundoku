import type { Meta, StoryObj } from '@storybook/vue3'
import CategoriesPane from './CategoriesPane.vue'
import { settingsCategories } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Categories CRUD pane. Flip the Storybook theme toolbar to
 * confirm both dark and light. The rename/delete confirm modals open on action.
 */
const meta = {
  title: 'Settings/CategoriesPane',
  component: CategoriesPane,
  parameters: { layout: 'padded' },
  args: { categories: settingsCategories },
} satisfies Meta<typeof CategoriesPane>

export default meta
type Story = StoryObj<typeof meta>

/** The seed category list — "Other" is protected + the default landing. */
export const Default: Story = {
  args: { categoryAction: { busyId: null } },
}

/**
 * §16: one row mid-mutation (busy spinner + disabled controls) plus a failed-move
 * error surfaced inline, not just a silent spinner.
 */
export const Busy: Story = {
  args: {
    categoryAction: { busyId: 'cat-manhwa', error: 'Folder move failed — the target name already exists on disk.' },
  },
}
