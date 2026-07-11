import type { Meta, StoryObj } from '@storybook/vue3'
import ReaderStrip from './ReaderStrip.vue'
import { readerChapters, fakePageUrl } from '../../fixtures/reader'

/**
 * Stories for the long-strip scroller. The strip fills its container, so each
 * story renders inside a fixed-height framed viewport to show the scroll +
 * inter-chapter dividers. Page images come from a seeded placeholder service.
 *
 * `centered` / `chapter-finished` / `near-tail` are logged via Storybook actions;
 * in the app they wire to useReader (append) and Slice 3 (progress).
 */
const meta = {
  title: 'Reader/ReaderStrip',
  component: ReaderStrip,
  parameters: { layout: 'fullscreen' },
  decorators: [() => ({ template: '<div style="height:640px;border:1px solid var(--border)"><story /></div>' })],
  args: {
    pageUrl: fakePageUrl,
    hasPrev: false,
    scrollRequest: null,
  },
} satisfies Meta<typeof ReaderStrip>

export default meta
type Story = StoryObj<typeof meta>

/** Mid-series: two chapters mounted out of four — scroll to reach the seam + tail sentinel. */
export const MidSeries: Story = {
  args: {
    chapters: readerChapters,
    mountedChapters: readerChapters.slice(0, 2),
  },
}

/** Last chapter mounted: the final divider shows the "End of downloaded chapters" message. */
export const LastChapter: Story = {
  args: {
    chapters: readerChapters,
    mountedChapters: readerChapters.slice(3),
  },
}

/** A chapter precedes the mounted window — the head sentinel is live (`hasPrev`),
 *  so scrolling to the very top prepends the previous chapter. */
export const HasPreviousChapter: Story = {
  args: {
    chapters: readerChapters,
    mountedChapters: readerChapters.slice(1, 3),
    hasPrev: true,
  },
}

/** Resume: opens scrolled to a mid-chapter last-read page (published as the
 *  strip's `scrollRequest`, token 1 — mirrors the route's resume anchor). */
export const ResumeMidChapter: Story = {
  args: {
    chapters: readerChapters,
    mountedChapters: readerChapters.slice(0, 2),
    scrollRequest: { chapterId: 'ch-1', page: 2, token: 1 },
  },
}
