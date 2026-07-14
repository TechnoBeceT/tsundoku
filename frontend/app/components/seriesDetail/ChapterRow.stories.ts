import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
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

/**
 * Set as current progress (QCAT-242, entry point B): a known-number chapter
 * renders the target icon button in `.chapter__controls`, alongside "Read"
 * and the state badge. The row only EMITS `set-current` — the confirm dialog
 * (`SetChapterProgressDialog`, its own story) and the actual mutation live on
 * the page, so this story just proves the control is present and clickable.
 */
export const SetCurrentProgress: Story = {
  args: { chapter: richSeries.chapters[0]! },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Set chapter .* as current progress/ }))
  },
}

/** Unknown number (no `set-current` target): the action is hidden — a chapter with no known number can't be a reset target. */
export const NoCurrentProgressAction: Story = {
  args: { chapter: richSeries.chapters[8]! },
}
