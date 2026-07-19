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
 * erroring / cooling-down / never-warmed / cold rows), the empty and loading
 * states, plus the warm-up and breaker-reset §16 states.
 */
const meta = {
  title: 'Health/SourceMetricsPane',
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
 * §16: a warm-up pass in flight (the Warm-now button spins) with the just-kicked-off
 * pass's "started" note shown above the list (the pass runs in the background).
 */
export const Warming: Story = {
  args: {
    warming: true,
    warmMessage: 'Warm-up started — sources warm in the background (this can take a few minutes)',
  },
}

/** §16: a failed warm-up surfaces its error inline — never fires into the void. */
export const WarmFailed: Story = {
  args: { warmError: 'Warm-up failed — could not reach the engine.' },
}

/**
 * A source's breaker reset in flight — the tripped ComicK row's Reset button
 * spins (`resetting` = its id) while the request runs.
 */
export const Resetting: Story = {
  args: { resetting: 'src-comick' },
}

/** §16: a failed breaker reset surfaces its error inline above the list. */
export const ResetFailed: Story = {
  args: { resetError: 'Reset failed — could not reach the engine.' },
}
