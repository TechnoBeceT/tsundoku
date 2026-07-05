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
