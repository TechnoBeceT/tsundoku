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

/** Unread: never opened — full-strength row + the unread dot. */
export const Unread: Story = {
  args: { chapter: richSeries.chapters[0]! },
}

/** Partially read: shows the "Page N / M" resume line (1-based display of the 0-based `lastReadPage`). */
export const PartiallyRead: Story = {
  args: { chapter: richSeries.chapters[1]! },
}

/** Read: the row dims and the unread dot disappears. */
export const Read: Story = {
  args: {
    chapter: {
      ...richSeries.chapters[0]!,
      read: true,
      lastReadPage: (richSeries.chapters[0]!.pageCount ?? 1) - 1,
      readAt: new Date().toISOString(),
    },
  },
}
