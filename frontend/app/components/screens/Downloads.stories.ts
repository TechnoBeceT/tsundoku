import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Downloads from './Downloads.vue'
import { downloadItems, failedItems, queuedItems } from '../../fixtures/downloads'
import type { DownloadTab } from './downloads.types'
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
 * The `counts` prop is now required for exact badges + bulk-action gating;
 * all interactive stories derive it from the fixture for correct badge display.
 * Existing stories that use `args` (FailedRetrying, Empty) omit `counts` and
 * receive the zero default (`{ active:0, failed:0, terminal:0, queued:0 }`).
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

/** Fixture-derived exact counts — matches the downloadItems fixture data. */
const fixtureCounts = {
  active: downloadItems.filter((i) => i.state === 'downloading' || i.state === 'upgrading').length,
  failed: downloadItems.filter((i) => i.state === 'failed').length,
  terminal: downloadItems.filter((i) => i.state === 'permanently_failed').length,
  queued: downloadItems.filter((i) => i.state === 'wanted' || i.state === 'upgrade_available').length,
}

/** Renders the screen with a live `activeTab` so the tab bar actually switches. */
const interactive = (startTab: DownloadTab, cycleActive = false, nextCycleMinutes: number | null = 14) => ({
  components: { Downloads },
  setup() {
    const activeTab = ref<DownloadTab>(startTab)
    return { activeTab, downloadItems, cycleActive, nextCycleMinutes, fixtureCounts }
  },
  template: `
    <Downloads
      :items="downloadItems"
      :active-tab="activeTab"
      :cycle-active="cycleActive"
      :next-cycle-minutes="nextCycleMinutes"
      :counts="fixtureCounts"
      @set-tab="activeTab = $event"
    />
  `,
})

/** Active tab — in-flight rows with the indeterminate progress bar (cycle running). */
export const Active: Story = {
  render: () => interactive('active', true, null),
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
    nextCycleMinutes: 14,
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
    counts: { active: 2, failed: 5, terminal: 1, queued: 250 },
  },
}
