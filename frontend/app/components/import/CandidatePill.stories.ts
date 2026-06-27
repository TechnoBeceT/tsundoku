import type { Meta, StoryObj } from '@storybook/vue3'
import CandidatePill from './CandidatePill.vue'
import { searchResults } from '../../fixtures/import'

/**
 * Stories for one source candidate pill (a cell inside a <SearchGroupCard>).
 * `WithCover` shows the lazy cover image; `NoCover` exercises the <CoverImage>
 * initial-letter placeholder. Flip the Storybook theme toolbar to confirm both
 * themes.
 */
const meta = {
  title: 'Import/CandidatePill',
  component: CandidatePill,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof CandidatePill>

export default meta
type Story = StoryObj<typeof meta>

const withCover = searchResults[0]!.candidates[0]! // MangaDex — has a thumbnail
const noCover = searchResults[0]!.candidates[2]! // Manganato — empty thumbnail → placeholder

/** A candidate with a cover image. */
export const WithCover: Story = {
  args: { candidate: withCover },
}

/** A candidate with no cover URL → the initial-letter placeholder. */
export const NoCover: Story = {
  args: { candidate: noCover },
}
