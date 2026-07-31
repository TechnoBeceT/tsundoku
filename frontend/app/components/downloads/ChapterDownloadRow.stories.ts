import type { Meta, StoryObj } from '@storybook/vue3'
import ChapterDownloadRow from './ChapterDownloadRow.vue'
import ProgressBar from '../ui/ProgressBar.vue'
import { downloadItems } from '../../fixtures/downloads'

/**
 * Stories for ChapterDownloadRow — the shared download-activity row used by all
 * three Downloads tabs. Covers the cover image vs the branded placeholder, the
 * category chip + meta line, the chapter-state badge, and the `before-badge`
 * slot (where each tab injects its trailing content). Flip the theme toolbar to
 * confirm the token-only palette reads on both surfaces.
 */
const meta = {
  title: 'Downloads/ChapterDownloadRow',
  component: ChapterDownloadRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ChapterDownloadRow>

export default meta
type Story = StoryObj<typeof meta>

// A row with a real cover (Solo Leveling, downloading).
const withCover = downloadItems[0]!
// A row whose cover is empty → the branded placeholder (Berserk, upgrading).
const noCover = downloadItems[1]!

/** Default row with a cover image and the downloading badge. */
export const Default: Story = {
  args: { item: withCover },
}

/** Empty cover → the inverse BrandMark placeholder. */
export const PlaceholderCover: Story = {
  args: { item: noCover },
}

/**
 * An UPGRADING row: the meta line names both sides of the convergence —
 * "MangaDex → Asura Scans" — so during an upgrade wave the owner sees where each
 * chapter is HEADED, not the source being replaced. The attempt badge names the
 * TARGET being fetched ("Asura Scans · 1/5"), NOT the satisfier it replaces (whose
 * "MangaDex · 0/5" would misreport the upgrade). A row with no `upgradeTarget`
 * (every other story) shows just its current source + that source's badge.
 */
export const UpgradeTarget: Story = {
  // the Berserk fixture is upgrading → Asura Scans; enrich it with a retry budget +
  // the target's spent attempts so the target-source badge renders.
  args: { item: { ...noCover, isUpgrade: true, maxRetries: 5, attempts: 0, upgradeTargetAttempts: 1 } },
}

/**
 * A SOURCELESS row: no provider's feed carries this chapter, so nothing is
 * fetching it and the backend reports an empty `providerName`. The meta line shows
 * an em-dash rather than crediting a source that does not offer the chapter — which
 * is how a stuck, unfetchable chapter becomes visible.
 */
export const NoSource: Story = {
  args: {
    item: { ...withCover, state: 'wanted', provider: '', providerName: '', upgradeTarget: undefined },
  },
}

/** With a `before-badge` slot — the Active tab's indeterminate progress bar. */
export const WithProgressSlot: Story = {
  render: (args) => ({
    components: { ChapterDownloadRow },
    setup: () => ({ args }),
    template: `
      <ChapterDownloadRow v-bind="args">
        <template #before-badge>
          <div style="width:90px;height:5px;border-radius:var(--radius-pill);background:var(--surface3);flex:none" />
        </template>
      </ChapterDownloadRow>
    `,
  }),
  args: { item: withCover },
}

/**
 * Determinate progress — the live Active row once a `download.progress` event has
 * arrived: the bar fills to 30% (12 of 40 pages) with the "12 / 40" page counter
 * beneath it, exactly as Downloads.vue composes the shared ProgressBar atom.
 */
export const WithDeterminateProgress: Story = {
  render: (args) => ({
    components: { ChapterDownloadRow, ProgressBar },
    setup: () => ({ args }),
    template: `
      <ChapterDownloadRow v-bind="args">
        <template #before-badge>
          <div style="width:90px;flex:none;display:flex;flex-direction:column;gap:4px">
            <ProgressBar :value="30" tone="linear-gradient(90deg, var(--accent), var(--accentBright))" />
            <span style="font-size:10.5px;font-weight:var(--weight-bold);color:var(--faint);text-align:right;font-variant-numeric:tabular-nums">12 / 40</span>
          </div>
        </template>
      </ChapterDownloadRow>
    `,
  }),
  args: { item: withCover },
}

/**
 * EARLY ACCESS (GAP-141): the source is deliberately withholding this chapter
 * behind coins for a few days. Its `state` is still `failed` — the fetch produced
 * no file — but nothing is broken, so the row swaps the red "Failed" pill for a calm
 * "Early access · free ~3d" badge and drops the attempt badge (a paywall never
 * spends the budget, so "0/5" would only prompt "why did it fail with zero tries?").
 */
export const EarlyAccess: Story = {
  args: {
    item: {
      ...withCover,
      state: 'failed',
      providerName: 'Hive Scans',
      isUpgrade: false,
      upgradeTarget: undefined,
      attempts: 0,
      maxRetries: 5,
      locked: true,
      lockedUntil: new Date(Date.now() + 3 * 24 * 3_600_000).toISOString(),
      lastError: 'upstream error: Chapter locked, coins required',
    },
  },
}
