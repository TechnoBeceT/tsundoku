import type { Meta, StoryObj } from '@storybook/vue3'
import HBarChart from './HBarChart.vue'
import { reportOverview } from '../../fixtures/sourceReport'
import { formatDurationMs } from '~/utils/timeFormat'

/**
 * Stories for the horizontal-bar leaderboard. Single series → no legend, direct
 * value labels, one shared scale. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/HBarChart',
  component: HBarChart,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof HBarChart>

export default meta
type Story = StoryObj<typeof meta>

/** Slowest-sources leaderboard (amber), from the fixture overview. */
export const SlowestSources: Story = {
  args: {
    tone: 'var(--set-update-text)',
    items: reportOverview.slowestSources.map(s => ({
      key: s.sourceKey,
      label: s.sourceName,
      value: s.ewmaLatencyMs,
      valueLabel: formatDurationMs(s.ewmaLatencyMs),
    })),
  },
}

/** Failing-sources leaderboard (rose). */
export const FailingSources: Story = {
  args: {
    tone: 'var(--danger)',
    items: reportOverview.failingSources.map(s => ({
      key: s.sourceKey,
      label: s.sourceKey,
      value: s.consecutiveFailures,
      valueLabel: `${s.consecutiveFailures}×`,
    })),
  },
}

/** Empty — the all-clear message. */
export const Empty: Story = {
  args: { items: [], emptyLabel: 'No source is failing — all clear.' },
}
