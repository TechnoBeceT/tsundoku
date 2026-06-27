import type { Meta, StoryObj } from '@storybook/vue3'
import ChapterRow from './ChapterRow.vue'
import { richSeries } from '../../fixtures/seriesDetail'

/**
 * Stories for one chapter-table row. Chapters are pulled from the shared Series
 * Detail fixture so the StatusBadge hues match the screen. Flip the Storybook
 * theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'SeriesDetail/ChapterRow',
  component: ChapterRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ChapterRow>

export default meta
type Story = StoryObj<typeof meta>

/** Downloaded: a named chapter with its CBZ filename + page count + "On disk" badge. */
export const Downloaded: Story = {
  args: { chapter: richSeries.chapters[0]! },
}

/** Upgrade available: a better source is now ranked higher. */
export const UpgradeAvailable: Story = {
  args: { chapter: richSeries.chapters[2]! },
}

/** Wanted, no resolved name: falls back to "Chapter N", no filename/pages. */
export const WantedNoName: Story = {
  args: { chapter: richSeries.chapters[5]! },
}

/** Permanently failed: the terminal failure state. */
export const PermanentlyFailed: Story = {
  args: { chapter: richSeries.chapters[7]! },
}

/** Unknown number: the em-dash number placeholder. */
export const UnknownNumber: Story = {
  args: { chapter: richSeries.chapters[8]! },
}
