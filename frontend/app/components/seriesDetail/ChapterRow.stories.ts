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

/**
 * Upgrade available: a better source is now ranked higher. The old CBZ is still
 * on disk, so the "Read" button stays visible (readable state, not `downloaded`).
 */
export const UpgradeAvailable: Story = {
  args: { chapter: richSeries.chapters[2]! },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    // Readable-state chapters keep the Read button (the bug fix): the reader
    // opens `upgrade_available`/`upgrading`, not just `downloaded`.
    await canvas.findByRole('button', { name: 'Read' })
  },
}

/**
 * Upgrading: the replacement fetch is in flight; the old CBZ likewise stays on
 * disk, so the chapter remains readable and the "Read" button shows.
 */
export const Upgrading: Story = {
  args: { chapter: richSeries.chapters[3]! },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await canvas.findByRole('button', { name: 'Read' })
  },
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

/**
 * Re-download (QCAT-343): a DOWNLOADED chapter carries a refresh action to the
 * RIGHT of the "On disk" badge. It re-queues the chapter so the engine fetches it
 * again over the existing CBZ — the remedy when the stored bytes turn out wrong
 * while every state field says the chapter is fine.
 *
 * The row only EMITS `redownload`; the page runs the mutation. There is no
 * confirm gate and none is wanted: the action deletes nothing, so the old file
 * stays readable until its replacement lands.
 */
export const Redownload: Story = {
  args: { chapter: richSeries.chapters[0]! },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Re-download chapter/ }))
  },
}

/** Re-download in flight (§16): the action disables and shows its busy glyph. */
export const RedownloadInFlight: Story = {
  args: { chapter: richSeries.chapters[0]!, redownloading: true },
}

/**
 * No re-download action: an `upgrade_available` chapter still has an old CBZ on
 * disk (so "Read" shows) but is mid-convergence and owned by the engine, so the
 * API refuses it — the control is deliberately narrower than "Read".
 */
export const NoRedownloadAction: Story = {
  args: { chapter: richSeries.chapters[2]! },
}

/**
 * Re-download PENDING: the chapter is back at `wanted` (badge reads "Queued")
 * but its CBZ was deliberately KEPT, so it is still readable — readability
 * follows the file, not the state (`isReadableChapter`). The re-download control
 * itself is gone, since the API only accepts a `downloaded` chapter.
 */
export const RedownloadPendingStillReadable: Story = {
  args: { chapter: { ...richSeries.chapters[0]!, state: 'wanted' } },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await canvas.findByRole('button', { name: 'Read' })
  },
}
