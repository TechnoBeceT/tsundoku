import type { Meta, StoryObj } from '@storybook/vue3'
import SourceMetricRow from './SourceMetricRow.vue'
import { sourceMetrics } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for a single source-metric line. Flip the Storybook theme toolbar to
 * confirm both dark and light. Warm/cold is derived from `lastWarmedAt` age, so
 * the fixture timestamps are relative to now.
 */
const meta = {
  title: 'Settings/SourceMetricRow',
  component: SourceMetricRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SourceMetricRow>

export default meta
type Story = StoryObj<typeof meta>

/** Fast + warm — low latency, high success rate, recently warmed. */
export const Fast: Story = {
  args: { source: sourceMetrics[4]! },
}

/** Slow + warm — the backend flags it slow (amber "Slow" badge). */
export const Slow: Story = {
  args: { source: sourceMetrics[0]! },
}

/** Erroring — a danger badge + the truncated last-error line (full text on hover). */
export const Erroring: Story = {
  args: { source: sourceMetrics[1]! },
}

/** Never warmed — unmeasured source, neutral badge, latency reads "—". */
export const NeverWarmed: Story = {
  args: { source: sourceMetrics[2]! },
}

/** Healthy but cold — a lapsed warm-up session (warmed ~45 min ago). */
export const Cold: Story = {
  args: { source: sourceMetrics[3]! },
}
