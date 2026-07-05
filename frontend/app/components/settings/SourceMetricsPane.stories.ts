import type { Meta, StoryObj } from '@storybook/vue3'
import SourceMetricsPane from './SourceMetricsPane.vue'
import { sourceMetrics } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Source Metrics pane. Flip the Storybook theme toolbar to
 * confirm both dark and light. Covers the populated list (a mix of fast / slow /
 * erroring / never-warmed / cold rows) plus the empty and loading states.
 */
const meta = {
  title: 'Settings/SourceMetricsPane',
  component: SourceMetricsPane,
  parameters: { layout: 'padded' },
  args: { metrics: sourceMetrics },
} satisfies Meta<typeof SourceMetricsPane>

export default meta
type Story = StoryObj<typeof meta>

/** Populated — five sources spanning fast, slow, erroring, never-warmed, cold. */
export const Populated: Story = {}

/** Empty — no metrics recorded yet (the empty-state hint). */
export const Empty: Story = {
  args: { metrics: [] },
}

/** Loading — the pane's own skeleton rows while the list fetches. */
export const Pending: Story = {
  args: { metrics: [], pending: true },
}

/**
 * §16: a warm-up pass in flight (the Warm-now button spins) with the previous
 * pass's success note shown above the list.
 */
export const Warming: Story = {
  args: { warming: true, warmMessage: 'Warmed 12 sources' },
}

/** §16: a failed warm-up surfaces its error inline — never fires into the void. */
export const WarmFailed: Story = {
  args: { warmError: 'Warm-up failed — 502 from the engine.' },
}
