import type { Meta, StoryObj } from '@storybook/vue3'
import EventTypeBreakdown from './EventTypeBreakdown.vue'
import { reportOverview, sourceReports } from '../../fixtures/sourceReport'

/**
 * Stories for the per-operation breakdown. Each bar's emerald fill is the success
 * share over the rose "failed" bed. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/EventTypeBreakdown',
  component: EventTypeBreakdown,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof EventTypeBreakdown>

export default meta
type Story = StoryObj<typeof meta>

/** The library-wide breakdown. */
export const Overview: Story = {
  args: { items: reportOverview.eventsByType },
}

/** A single failing source's breakdown (downloads collapsing). */
export const FailingSource: Story = {
  args: { items: sourceReports[0]!.byType },
}

/** Empty. */
export const Empty: Story = {
  args: { items: [] },
}
