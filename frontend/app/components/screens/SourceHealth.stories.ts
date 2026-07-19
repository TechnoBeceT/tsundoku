import type { Meta, StoryObj } from '@storybook/vue3'
import SourceHealth from './SourceHealth.vue'
import { sourceMetrics } from '../../fixtures/settings'
// Load the source-metric status tokens directly so the metric badges (warm/cold,
// slow, cooling-down) render with the real palette in isolation. The live app +
// Storybook preview both pull these via index.css; this side-effect keeps the
// story self-sufficient (mirrors the metric-row/pane stories).
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Source Health tab (Tab 2 of the `/health` console) — the
 * relocated source-metrics UI. Flip the Storybook theme toolbar to confirm both
 * dark and light. The Kaizoku-grade report sections are slice 4 and not shown.
 */
const meta = {
  title: 'Screens/SourceHealth',
  component: SourceHealth,
  parameters: { layout: 'fullscreen' },
  args: { metrics: sourceMetrics },
} satisfies Meta<typeof SourceHealth>

export default meta
type Story = StoryObj<typeof meta>

/** Populated — a mix of fast / slow / erroring / cooling-down / never-warmed rows. */
export const Populated: Story = {}

/** Empty — no metrics recorded yet (the empty-state hint). */
export const Empty: Story = {
  args: { metrics: [] },
}

/** Loading — the pane's own skeleton rows while the list fetches. */
export const Loading: Story = {
  args: { metrics: [], pending: true },
}
