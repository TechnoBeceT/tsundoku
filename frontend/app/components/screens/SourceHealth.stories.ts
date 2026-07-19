import type { Meta, StoryObj } from '@storybook/vue3'
import SourceHealth from './SourceHealth.vue'
import { sourceMetrics } from '../../fixtures/settings'
import { mockReportModel } from '../../fixtures/sourceReport'
// Load the source-metric status tokens directly so the metric badges (warm/cold,
// slow, cooling-down) render with the real palette in isolation. The live app +
// Storybook preview both pull these via index.css; this side-effect keeps the
// story self-sufficient (mirrors the metric-row/pane stories).
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Source Health tab (Tab 2 of the `/health` console): the
 * Kaizoku-grade Source Metrics report (KPI cards → leaderboards → recent errors →
 * per-source accordion → event log) stacked above the relocated search-metrics
 * pane. The report arrives as one bundle prop; these stories pass a mock model.
 * Flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Screens/SourceHealth',
  component: SourceHealth,
  parameters: { layout: 'fullscreen' },
  args: { metrics: sourceMetrics, report: mockReportModel() },
} satisfies Meta<typeof SourceHealth>

export default meta
type Story = StoryObj<typeof meta>

/** The full report — a mixed-health library with one source (ComicK) failing. */
export const Populated: Story = {}

/** A source expanded — its timeline (the cliff), breakdown, and recent events. */
export const SourceExpanded: Story = {
  args: { report: mockReportModel({ expandedKey: 'ComicK' }) },
}

/** The forensic modal open on a diagnosed captcha failure. */
export const EventDetail: Story = {
  args: {
    report: mockReportModel({
      eventModalOpen: true,
      selectedEvent: mockReportModel().overview!.recentErrors[0]!,
    }),
  },
}

/** Loading — the report's KPI + row skeletons while it fetches. */
export const ReportLoading: Story = {
  args: { report: mockReportModel({ reportPending: true, overview: null, sources: [] }) },
}

/** Report load failed — the inline error banner (§16). */
export const ReportError: Story = {
  args: { report: mockReportModel({ reportError: 'Failed to load the source report', overview: null, sources: [] }) },
}

/** No report bundle — only the relocated metrics pane shows (the slice-3 shape). */
export const MetricsPaneOnly: Story = {
  args: { report: null },
}

/** Empty metrics list (the pane's empty state) with a populated report above. */
export const EmptyMetrics: Story = {
  args: { metrics: [] },
}
