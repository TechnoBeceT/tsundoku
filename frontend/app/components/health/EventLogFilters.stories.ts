import type { Meta, StoryObj } from '@storybook/vue3'
import EventLogFilters from './EventLogFilters.vue'

/**
 * Stories for the event-log toolbar (filters + pager). Presentation-only — the
 * parent refetches on each change. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/EventLogFilters',
  component: EventLogFilters,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof EventLogFilters>

export default meta
type Story = StoryObj<typeof meta>

/** First page of a multi-page unfiltered feed. */
export const FirstPage: Story = {
  args: { status: '', eventType: '', page: 0, pageCount: 6, total: 284 },
}

/** A middle page with a failures + downloads filter applied. */
export const Filtered: Story = {
  args: { status: 'failed', eventType: 'download', page: 2, pageCount: 4, total: 173 },
}

/** The last page (Next disabled). */
export const LastPage: Story = {
  args: { status: '', eventType: '', page: 5, pageCount: 6, total: 284 },
}

/** Loading — the pager is disabled while a page fetches. */
export const Pending: Story = {
  args: { status: '', eventType: '', page: 1, pageCount: 6, total: 284, pending: true },
}
