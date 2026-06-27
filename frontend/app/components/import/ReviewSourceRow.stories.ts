import type { Meta, StoryObj } from '@storybook/vue3'
import ReviewSourceRow from './ReviewSourceRow.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for one Stage-3 review line. `Preferred` is the rank-1 source (accent
 * rank badge + the "PREFERRED" <Tag>); `Secondary` is a lower-ranked source.
 * Flip the Storybook theme toolbar to confirm both themes.
 */
const meta = {
  title: 'Import/ReviewSourceRow',
  component: ReviewSourceRow,
  parameters: { layout: 'padded' },
  decorators: [() => ({ template: '<div style="max-width:780px"><story /></div>' })],
} satisfies Meta<typeof ReviewSourceRow>

export default meta
type Story = StoryObj<typeof meta>

const candidate = searchResults[0]!.candidates[0]!

/** The preferred (rank-1) source — accent badge + PREFERRED tag. */
export const Preferred: Story = {
  args: { candidate, rank: 1, importance: 30, preferred: true },
}

/** A lower-ranked source — neutral badge, no PREFERRED tag. */
export const Secondary: Story = {
  args: { candidate, rank: 2, importance: 20, preferred: false },
}
