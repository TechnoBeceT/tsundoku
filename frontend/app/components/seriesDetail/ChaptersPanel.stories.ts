import type { Meta, StoryObj } from '@storybook/vue3'
import ChaptersPanel from './ChaptersPanel.vue'
import { richSeries } from '../../fixtures/seriesDetail'

/**
 * Stories for the Series Detail "Chapters" card — the titled, count-pilled,
 * scrolling list of chapter rows. The fixture walks every download state so the
 * full badge palette renders. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/ChaptersPanel',
  component: ChaptersPanel,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ChaptersPanel>

export default meta
type Story = StoryObj<typeof meta>

/** The full chapter feed — every state, plus an unknown-number row. */
export const Default: Story = {
  args: { chapters: richSeries.chapters, total: richSeries.chapterCounts.total },
}

/** A short feed — a couple of on-disk chapters. */
export const Short: Story = {
  args: { chapters: richSeries.chapters.slice(0, 2), total: 2 },
}
