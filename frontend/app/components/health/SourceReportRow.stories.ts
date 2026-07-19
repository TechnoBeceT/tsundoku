import type { Meta, StoryObj } from '@storybook/vue3'
import SourceReportRow from './SourceReportRow.vue'
import { sourceReports, timelineBuckets, sourceEvents } from '../../fixtures/sourceReport'
import { sourceMetrics } from '../../fixtures/settings'
// The metric row inside reads the source-metric status tokens (warm/cold, slow,
// cooling) — pulled via index.css in the app + preview; kept here for isolation.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for one per-source accordion row. The collapsed summary REUSES
 * SourceMetricRow, so every Tsundoku superset badge (warm/cold, slow, erroring,
 * cooling-down + Reset) is preserved; expanding reveals the event-sourced report.
 * Flip the theme toolbar.
 */
const meta = {
  title: 'Health/SourceReportRow',
  component: SourceReportRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SourceReportRow>

export default meta
type Story = StoryObj<typeof meta>

// ComicK: a failing source, tripped breaker — its metric snapshot carries the
// cooling-down banner + Reset (sourceMetrics[1] = "ComicK").
const comickReport = sourceReports[0]!
const comickMetric = sourceMetrics[1]!

/** Collapsed — the metric summary with the cooling-down breaker banner + Reset. */
export const Collapsed: Story = {
  args: { report: comickReport, metric: comickMetric, expanded: false },
}

/** Expanded — timeline (the cliff), per-operation breakdown, and recent events. */
export const Expanded: Story = {
  args: {
    report: comickReport,
    metric: comickMetric,
    expanded: true,
    timeline: timelineBuckets,
    recentEvents: sourceEvents.slice(0, 5),
  },
}

/** Expanded, timeline still loading. */
export const ExpandedLoading: Story = {
  args: {
    report: comickReport,
    metric: comickMetric,
    expanded: true,
    timelinePending: true,
    eventsPending: true,
  },
}

/** A healthy source, collapsed. */
export const Healthy: Story = {
  args: { report: sourceReports[2]!, metric: sourceMetrics[4]!, expanded: false },
}

/** Fallback — a source with events but no metrics snapshot yet (no metric row). */
export const NoMetric: Story = {
  args: { report: comickReport, metric: null, expanded: false },
}
