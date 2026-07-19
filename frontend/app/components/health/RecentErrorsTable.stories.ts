import type { Meta, StoryObj } from '@storybook/vue3'
import RecentErrorsTable from './RecentErrorsTable.vue'
import { recentErrors } from '../../fixtures/sourceReport'

/**
 * Stories for the recent-errors preview. Rows are clickable (open the forensic
 * detail). Flip the theme toolbar.
 */
const meta = {
  title: 'Health/RecentErrorsTable',
  component: RecentErrorsTable,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof RecentErrorsTable>

export default meta
type Story = StoryObj<typeof meta>

/** Populated — a spread of recent failures. */
export const Populated: Story = {
  args: { errors: recentErrors },
}

/** Empty — the good state (every source behaving). */
export const Empty: Story = {
  args: { errors: [] },
}
