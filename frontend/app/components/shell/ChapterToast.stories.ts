import type { Meta, StoryObj } from '@storybook/vue3'
import ChapterToast from './ChapterToast.vue'

/**
 * Stories for the in-app new-chapter toast. Flip the Storybook theme toolbar to
 * confirm both dark and light. Covers the single-series and the digest bodies.
 */
const meta = {
  title: 'Shell/ChapterToast',
  component: ChapterToast,
  parameters: { layout: 'fullscreen' },
  args: { title: 'Solo Leveling', body: '1 new chapter' },
} satisfies Meta<typeof ChapterToast>

export default meta
type Story = StoryObj<typeof meta>

/** Single series — one new chapter. */
export const SingleSeries: Story = {}

/** Digest — many chapters across several series. */
export const Digest: Story = {
  args: { title: 'New chapters', body: '12 new chapters across 5 series' },
}
