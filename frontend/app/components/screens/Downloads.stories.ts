import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Downloads from './Downloads.vue'
import { downloadItems, failedItems, queuedItems, queuedDeferredItems } from '../../fixtures/downloads'
import type { DownloadTab } from './downloads.types'
import type { CycleTimer } from '../../utils/cycleSchedule'
// Load this screen's state-badge tokens directly: index.css does not @import them
// yet (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/downloads.css'

/**
 * Stories for the Downloads screen — the three tabs (Active · Failed · Queued)
 * over one flat chapter-activity list. Flip the Storybook theme toolbar to
 * confirm it reads correctly in BOTH dark and light. Each story opens on its
 * tab; the tab bar is interactive (clicking re-filters the shared fixture).
 *
 * The `counts` prop drives the badges + bulk-action gating; all interactive
 * stories derive it from the fixture for correct badge display. Stories that omit
 * `counts` receive the zero default (`{ active:0, queued:0, allFailures:0 }`); the
 * retryable/terminal sub-tab badges are derived from the loaded items regardless.
 */
const meta = {
  title: 'Screens/Downloads',
  component: Downloads,
  parameters: { layout: 'fullscreen' },
  // items is a required prop; the interactive stories pass the fixture in their
  // render template, so this default only satisfies the CSF3 story typing.
  args: { items: downloadItems },
} satisfies Meta<typeof Downloads>

export default meta
type Story = StoryObj<typeof meta>

/** The discovery sweep, mid-countdown — shared by every interactive story. */
const refreshCycle: CycleTimer = { state: 'waiting', remainingMs: 6_728_000 }

/** Fixture-derived counts — matches the downloadItems fixture data. */
const fixtureCounts = {
  active: downloadItems.filter((i) => i.state === 'downloading' || i.state === 'upgrading').length,
  queued: downloadItems.filter((i) => i.state === 'wanted' || i.state === 'upgrade_available').length,
  allFailures: downloadItems.filter(
    (i) => i.state === 'failed' || i.state === 'permanently_failed' || (i.failingProviderName ?? '') !== '',
  ).length,
}

/**
 * Renders the screen with a live `activeTab` so the tab bar actually switches.
 * `downloadCycle` is the download loop's state as useCycleTimers derives it from
 * the server's schedule; it defaults to a plain countdown.
 */
const interactive = (
  startTab: DownloadTab,
  downloadCycle: CycleTimer = { state: 'waiting', remainingMs: 843_000 },
) => ({
  components: { Downloads },
  setup() {
    const activeTab = ref<DownloadTab>(startTab)
    return { activeTab, downloadItems, downloadCycle, refreshCycle, fixtureCounts }
  },
  template: `
    <Downloads
      :items="downloadItems"
      :active-tab="activeTab"
      :download-cycle="downloadCycle"
      :refresh-cycle="refreshCycle"
      :counts="fixtureCounts"
      @set-tab="activeTab = $event"
    />
  `,
})

/** Active tab — in-flight rows with the indeterminate progress bar (cycle running). */
export const Active: Story = {
  render: () => interactive('active', { state: 'running', remainingMs: null }),
}

/**
 * The BACK-TO-BACK steady state: the cycle is taking longer than its configured
 * period, so the next run is already due and cycles run with no idle gap. Both the
 * banner and the header countdown say so (amber, "next due now") instead of one of
 * them claiming the engine is idle while rows are visibly downloading.
 */
export const CycleOverrunning: Story = {
  render: () => interactive('active', { state: 'overrunning', remainingMs: null }),
}

/**
 * The schedule endpoint could not be read: both pills state that plainly rather
 * than counting down from a schedule invented on the client.
 */
export const ScheduleUnavailable: Story = {
  args: {
    items: downloadItems,
    activeTab: 'active',
    downloadCycle: null,
    refreshCycle: null,
    counts: fixtureCounts,
  },
}

/** Failed tab — retryable + terminal rows, per-row retry + expandable errors. */
export const Failed: Story = {
  render: () => interactive('failed'),
}

/** Queued tab — wanted + upgrade_available rows, with the upgrades-only toggle. */
export const Scheduled: Story = {
  render: () => interactive('queued'),
}

/**
 * Queued tab, DEFERRED: every waiting chapter's source is on a persisted cooldown
 * (the owner-reported "upgrades stuck" case). Each row reads "⏳ waiting on <source>
 * · retry ~Nm" in place of the bare UPGRADE tag / Wanted badge, and the top-right
 * pill states the honest "N waiting on a source · retry ~Nm" instead of "Idle".
 */
export const QueuedDeferred: Story = {
  args: {
    items: queuedDeferredItems,
    activeTab: 'queued',
    counts: { active: 0, queued: queuedDeferredItems.length, allFailures: 0 },
  },
}

/**
 * Failed tab mid-retry: one row's retry is in flight (button shows "Retrying…")
 * and a previous attempt surfaced an error banner — the §16 loading + error
 * states made visible, never fired into the void.
 */
export const FailedRetrying: Story = {
  args: {
    items: failedItems,
    activeTab: 'failed',
    retryingIds: ['c-0010'],
    retryError: 'Couldn\'t requeue chapter — Suwayomi returned 502. Try again in a moment.',
  },
}

/** Empty library — no chapter activity at all; each tab shows its own empty state. */
export const Empty: Story = {
  args: {
    items: [],
    activeTab: 'active',
    downloadCycle: { state: 'waiting', remainingMs: 843_000 },
  },
}

/**
 * Load-more affordance: queued tab with 3 of 250 chapters loaded.
 * The "Load more · 3 of 250" button is visible and actionable.
 * Counts are exact server totals (not derived from the loaded subset).
 */
export const WithLoadMore: Story = {
  args: {
    items: queuedItems,
    activeTab: 'queued',
    hasMore: true,
    total: 250,
    counts: { active: 2, queued: 250, allFailures: 6 },
  },
}

/**
 * "Download now" — idle state: the manual trigger button sits beside the cycle
 * banner, ready to kick an immediate cycle without waiting for the timer.
 */
export const DownloadNowIdle: Story = {
  args: {
    items: queuedItems,
    activeTab: 'queued',
    counts: { active: 2, queued: 3, allFailures: 6 },
  },
}

/**
 * "Download now" — busy + success states (§16): while `running` the button
 * shows a spinner + "Starting…"; once the request completes, `runMessage`
 * surfaces a quiet success note above the list (never a silent fire-and-forget).
 */
export const DownloadNowBusy: Story = {
  args: {
    items: queuedItems,
    activeTab: 'queued',
    counts: { active: 2, queued: 3, allFailures: 6 },
    running: true,
  },
}

export const DownloadNowStarted: Story = {
  args: {
    items: queuedItems,
    activeTab: 'queued',
    counts: { active: 2, queued: 3, allFailures: 6 },
    runMessage: 'Download cycle started',
  },
}

/** "Download now" failure — surfaced inline, never swallowed (§16). */
export const DownloadNowFailed: Story = {
  args: {
    items: queuedItems,
    activeTab: 'queued',
    counts: { active: 2, queued: 3, allFailures: 6 },
    runError: 'Failed to start download cycle',
  },
}
