import type { Meta, StoryObj } from '@storybook/vue3'
import StatCard from './StatCard.vue'

/**
 * Stories for the KPI tile. The tone drives both the value colour and the top
 * accent rule; pass a token-backed `var(--…)`. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/StatCard',
  component: StatCard,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof StatCard>

export default meta
type Story = StoryObj<typeof meta>

/** A healthy success rate — emerald. */
export const SuccessRate: Story = {
  args: { label: 'Success rate', value: '89%', tone: 'var(--set-ok-dot)', hint: '1,147 / 1,284 events' },
}

/** A failure count that needs attention — rose. */
export const Failures: Story = {
  args: { label: 'Failures', value: '137', tone: 'var(--danger)', hint: 'need attention' },
}

/** A plain neutral count. */
export const Neutral: Story = {
  args: { label: 'Operations', value: '1,284', hint: 'in the last 24h' },
}

/** No hint line. */
export const NoHint: Story = {
  args: { label: 'Active sources', value: 6 },
}
