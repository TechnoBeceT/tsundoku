/**
 * sourceReport.ts — mock data for the Source Metrics report (Storybook + vitest).
 * Mirrors the shapes the reporting API returns, with realistic source names and a
 * deliberate "failure cliff" on one source (ComicK) so the timeline histogram and
 * the failing-source leaderboard have something to show. Timestamps are relative
 * to load time so the relative-time labels read sensibly in Storybook.
 */
import type { SourceHealthReportModel } from '~/composables/useSourceHealthReport'
import { sourceMetrics } from '~/fixtures/settings'
import type {
  ReportOverview,
  SourceEventRecord,
  SourceReport,
  TimelineBucket,
} from '~/components/health/sourceReport.types'

const now = Date.now()
const HOUR = 3_600_000
const MIN = 60_000
const agoIso = (ms: number): string => new Date(now - ms).toISOString()
const inIso = (ms: number): string => new Date(now + ms).toISOString()

/** A spread of recent failed events, newest first — the recent-errors preview. */
export const recentErrors: SourceEventRecord[] = [
  {
    id: 'evt-1',
    sourceKey: 'ComicK',
    sourceId: '42',
    sourceName: 'ComicK',
    language: 'en',
    eventType: 'download',
    status: 'failed',
    durationMs: 60_200,
    errorMessage: 'context deadline exceeded: FlareSolverr timed out after 60s while solving the Cloudflare challenge',
    errorCategory: 'captcha',
    itemsCount: null,
    metadata: { series: 'Solo Leveling', chapter: '179', url: 'https://comick.io/…' },
    createdAt: agoIso(3 * MIN),
  },
  {
    id: 'evt-2',
    sourceKey: 'Asura Scans',
    sourceId: '7',
    sourceName: 'Asura Scans',
    language: 'en',
    eventType: 'search',
    status: 'failed',
    durationMs: 429,
    errorMessage: 'request failed with status 429 too many requests',
    errorCategory: 'rate_limit',
    itemsCount: null,
    metadata: { keyword: 'the beginning after the end' },
    createdAt: agoIso(11 * MIN),
  },
  {
    id: 'evt-3',
    sourceKey: 'ComicK',
    sourceId: '42',
    sourceName: 'ComicK',
    language: 'en',
    eventType: 'download',
    status: 'failed',
    durationMs: 61_000,
    errorMessage: 'context deadline exceeded',
    errorCategory: 'timeout',
    itemsCount: null,
    metadata: { series: 'Omniscient Reader', chapter: '201' },
    createdAt: agoIso(24 * MIN),
  },
  {
    id: 'evt-4',
    sourceKey: 'Weeb Central',
    sourceId: '19',
    sourceName: 'Weeb Central',
    language: 'en',
    eventType: 'refresh',
    status: 'failed',
    durationMs: 1_800,
    errorMessage: 'parse error: unexpected end of JSON input',
    errorCategory: 'parse',
    itemsCount: null,
    metadata: { series: 'Jujutsu Kaisen' },
    createdAt: agoIso(52 * MIN),
  },
]

/** A healthy + a couple of failed successes for the global feed / source rows. */
export const sourceEvents: SourceEventRecord[] = [
  {
    id: 'evt-ok-1',
    sourceKey: 'MangaDex',
    sourceId: '2',
    sourceName: 'MangaDex',
    language: 'en',
    eventType: 'search',
    status: 'success',
    durationMs: 240,
    errorMessage: null,
    errorCategory: null,
    itemsCount: 18,
    metadata: { keyword: 'chainsaw man' },
    createdAt: agoIso(1 * MIN),
  },
  {
    id: 'evt-ok-2',
    sourceKey: 'MangaDex',
    sourceId: '2',
    sourceName: 'MangaDex',
    language: 'en',
    eventType: 'download',
    status: 'success',
    durationMs: 3_400,
    errorMessage: null,
    errorCategory: null,
    itemsCount: 24,
    metadata: { series: 'Chainsaw Man', chapter: '155' },
    createdAt: agoIso(6 * MIN),
  },
  {
    id: 'evt-ok-3',
    sourceKey: 'BiliBili Comics',
    sourceId: '31',
    sourceName: 'BiliBili Comics',
    language: 'en',
    eventType: 'warm',
    status: 'success',
    durationMs: 5_100,
    errorMessage: null,
    errorCategory: null,
    itemsCount: null,
    metadata: {},
    createdAt: agoIso(9 * MIN),
  },
  ...recentErrors,
]

/** The period overview — a mixed-health library with one source in trouble. */
export const reportOverview: ReportOverview = {
  period: '24h',
  since: agoIso(24 * HOUR),
  kpis: {
    totalEvents: 1_284,
    successEvents: 1_147,
    failedEvents: 137,
    successRate: 0.8933,
    activeSources: 6,
  },
  eventsByType: [
    { eventType: 'download', total: 720, success: 640, failed: 80 },
    { eventType: 'search', total: 380, success: 350, failed: 30 },
    { eventType: 'refresh', total: 140, success: 120, failed: 20 },
    { eventType: 'warm', total: 40, success: 37, failed: 3 },
    { eventType: 'breaker_trip', total: 3, success: 0, failed: 3 },
    { eventType: 'breaker_reset', total: 1, success: 1, failed: 0 },
  ],
  slowestSources: [
    { sourceKey: 'ComicK', sourceName: 'ComicK', ewmaLatencyMs: 18_400 },
    { sourceKey: 'Asura Scans', sourceName: 'Asura Scans', ewmaLatencyMs: 4_200 },
    { sourceKey: 'Weeb Central', sourceName: 'Weeb Central', ewmaLatencyMs: 2_600 },
    { sourceKey: 'BiliBili Comics', sourceName: 'BiliBili Comics', ewmaLatencyMs: 640 },
    { sourceKey: 'MangaDex', sourceName: 'MangaDex', ewmaLatencyMs: 240 },
  ],
  failingSources: [
    {
      sourceKey: 'ComicK',
      failingSince: agoIso(2 * HOUR),
      consecutiveFailures: 14,
      lastError: 'context deadline exceeded: FlareSolverr timed out after 60s',
      cooldownUntil: inIso(28 * MIN),
      isCoolingDown: true,
    },
    {
      sourceKey: 'Asura Scans',
      failingSince: agoIso(35 * MIN),
      consecutiveFailures: 4,
      lastError: 'request failed with status 429 too many requests',
      cooldownUntil: null,
      isCoolingDown: false,
    },
  ],
  recentErrors,
}

/** Per-source rollups for the accordion, failing-first. */
export const sourceReports: SourceReport[] = [
  {
    sourceKey: 'ComicK',
    sourceId: '42',
    sourceName: 'ComicK',
    language: 'en',
    totalEvents: 210,
    successEvents: 96,
    failedEvents: 114,
    successRate: 0.4571,
    byType: [
      { eventType: 'download', total: 150, success: 60, failed: 90 },
      { eventType: 'search', total: 48, success: 30, failed: 18 },
      { eventType: 'warm', total: 12, success: 6, failed: 6 },
    ],
    ewmaLatencyMs: 18_400,
    lastLatencyMs: 60_200,
    failingSince: agoIso(2 * HOUR),
    consecutiveFailures: 14,
    lastError: 'context deadline exceeded: FlareSolverr timed out after 60s while solving the Cloudflare challenge',
    cooldownUntil: inIso(28 * MIN),
    isCoolingDown: true,
  },
  {
    sourceKey: 'Asura Scans',
    sourceId: '7',
    sourceName: 'Asura Scans',
    language: 'en',
    totalEvents: 180,
    successEvents: 150,
    failedEvents: 30,
    successRate: 0.8333,
    byType: [
      { eventType: 'search', total: 120, success: 100, failed: 20 },
      { eventType: 'download', total: 60, success: 50, failed: 10 },
    ],
    ewmaLatencyMs: 4_200,
    lastLatencyMs: 5_100,
    failingSince: agoIso(35 * MIN),
    consecutiveFailures: 4,
    lastError: 'request failed with status 429 too many requests',
    cooldownUntil: null,
    isCoolingDown: false,
  },
  {
    sourceKey: 'MangaDex',
    sourceId: '2',
    sourceName: 'MangaDex',
    language: 'en',
    totalEvents: 640,
    successEvents: 632,
    failedEvents: 8,
    successRate: 0.9875,
    byType: [
      { eventType: 'download', total: 420, success: 416, failed: 4 },
      { eventType: 'search', total: 200, success: 197, failed: 3 },
      { eventType: 'refresh', total: 20, success: 19, failed: 1 },
    ],
    ewmaLatencyMs: 240,
    lastLatencyMs: 210,
    failingSince: null,
    consecutiveFailures: 0,
    lastError: '',
    cooldownUntil: null,
    isCoolingDown: false,
  },
]

/**
 * An hourly timeline with a clear "failure cliff": mostly-green early buckets, a
 * sharp rise in red at the end — the visual signature of "failing since ~2h ago".
 */
export const timelineBuckets: TimelineBucket[] = Array.from({ length: 24 }, (_, i) => {
  const hoursAgo = 23 - i
  const cliff = hoursAgo <= 2 // the last ~2 buckets go mostly red
  const success = cliff ? 2 : 8 + (i % 4)
  const failed = cliff ? 22 : (i % 5 === 0 ? 2 : 0)
  return {
    bucket: agoIso(hoursAgo * HOUR),
    success,
    failed,
    total: success + failed,
  }
})

/**
 * A ready-made report bundle for Storybook / component-test rendering of the
 * SourceHealth screen. Data-populated with the fixtures above and no-op methods,
 * so a story renders the whole report without a backend. Pass `overrides` to flip
 * a loading/empty/expanded state.
 */
export function mockReportModel(
  overrides: Partial<SourceHealthReportModel> = {},
): SourceHealthReportModel {
  const metricsByKey: Record<string, typeof sourceMetrics[number]> = {}
  for (const m of sourceMetrics) metricsByKey[m.name.trim()] = m
  return {
    overview: reportOverview,
    sources: sourceReports,
    reportPending: false,
    reportError: null,
    period: '24h',
    sort: 'failures',
    events: sourceEvents,
    eventsTotal: sourceEvents.length,
    eventsPage: 0,
    eventsPageCount: 1,
    eventStatus: '',
    eventType: '',
    eventLogPending: false,
    eventLogError: null,
    expandedKey: null,
    timeline: timelineBuckets,
    timelinePending: false,
    timelineBucket: 'hour',
    sourceEvents: sourceEvents.slice(0, 5),
    sourceEventsPending: false,
    selectedEvent: null,
    eventModalOpen: false,
    metricsByKey,
    load: () => { /* mock no-op */ },
    setPeriod: () => { /* mock no-op */ },
    setSort: () => { /* mock no-op */ },
    toggleSource: () => { /* mock no-op */ },
    setEventStatus: () => { /* mock no-op */ },
    setEventType: () => { /* mock no-op */ },
    eventsPrev: () => { /* mock no-op */ },
    eventsNext: () => { /* mock no-op */ },
    selectEvent: () => { /* mock no-op */ },
    closeEvent: () => { /* mock no-op */ },
    ...overrides,
  }
}
