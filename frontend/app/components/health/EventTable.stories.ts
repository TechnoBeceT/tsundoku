import type { Meta, StoryObj } from '@storybook/vue3'
import EventTable from './EventTable.vue'
import { sourceEvents } from '../../fixtures/sourceReport'

/**
 * Stories for the event-log table. Rows are clickable (open the forensic detail).
 * Covers populated (global + per-source), loading skeletons, and empty. Flip the
 * theme toolbar.
 */
const meta = {
  title: 'Health/EventTable',
  component: EventTable,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof EventTable>

export default meta
type Story = StoryObj<typeof meta>

/** The global feed — the source column is shown. */
export const Global: Story = {
  args: { events: sourceEvents, showSource: true },
}

/** A per-source feed — the source column is hidden (every row is one source). */
export const PerSource: Story = {
  args: { events: sourceEvents.slice(0, 5), showSource: false },
}

/** Loading — skeleton rows while the page fetches. */
export const Loading: Story = {
  args: { events: [], pending: true },
}

/** Empty — no events match the filter. */
export const Empty: Story = {
  args: { events: [] },
}
