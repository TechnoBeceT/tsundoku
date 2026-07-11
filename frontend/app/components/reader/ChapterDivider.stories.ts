import type { Meta, StoryObj } from '@storybook/vue3'
import ChapterDivider from './ChapterDivider.vue'

/**
 * Stories for the between-chapters seam. Mid-series shows the finished + next
 * chapter; last-chapter shows the explicit end-of-library message. Flip the
 * Storybook theme toolbar to confirm both themes.
 */
const meta = {
  title: 'Reader/ChapterDivider',
  component: ChapterDivider,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ChapterDivider>

export default meta
type Story = StoryObj<typeof meta>

/** Mid-series: a finished chapter (with a scanlator subtitle) flowing into the next. */
export const MidSeries: Story = {
  args: {
    finished: { number: 12, name: 'The Ant King', scanlator: 'Flame Scans' },
    next: { number: 13, name: 'Shadow Monarch' },
  },
}

/** Last chapter: no next → the explicit "End of downloaded chapters" message. */
export const LastChapter: Story = {
  args: {
    finished: { number: 179, name: 'Epilogue', scanlator: 'Flame Scans' },
  },
}

/** Unknown number: a null-number chapter shows just its title (no "Ch." prefix). */
export const UnknownNumber: Story = {
  args: {
    finished: { number: null, name: 'Bonus Story' },
    next: { number: 1, name: 'A New Beginning' },
  },
}
