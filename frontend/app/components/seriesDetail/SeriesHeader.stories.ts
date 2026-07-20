import type { Meta, StoryObj } from '@storybook/vue3'
import SeriesHeader from './SeriesHeader.vue'
import { categoryOptions, noCoverSeries, richSeries } from '../../fixtures/seriesDetail'

/**
 * Stories for the Series Detail header card — cover, category chip + title,
 * Delete button, the four chapter-count stats, the Monitored/Completed toggles,
 * and the category select. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/SeriesHeader',
  component: SeriesHeader,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SeriesHeader>

export default meta
type Story = StoryObj<typeof meta>

/** A rich series with a cover, monitored + not completed. */
export const Default: Story = {
  args: { series: richSeries, categoryOptions },
}

/** No cover URL — the branded placeholder fills the cover box. */
export const NoCover: Story = {
  args: { series: noCoverSeries, categoryOptions },
}

/** Saving: the toggles + category select are disabled mid-mutation. */
export const Saving: Story = {
  args: { series: richSeries, categoryOptions, saving: true },
}

/**
 * Stalled (QCAT-297): monitored + not completed but no new chapter from any
 * source in ~5 months — the "Last chapter …" line + the amber Stalled marker
 * render under the title. Purely informational.
 */
export const Stalled: Story = {
  args: {
    series: { ...richSeries, isStalled: true, latestChapterAt: '2026-02-14T00:00:00Z' },
    categoryOptions,
  },
}
