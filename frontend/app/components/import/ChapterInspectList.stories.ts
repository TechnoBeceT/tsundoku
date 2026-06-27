import type { Meta, StoryObj } from '@storybook/vue3'
import ChapterInspectList from './ChapterInspectList.vue'
import { inspectChapters } from '../../fixtures/import'

/**
 * Stories for the resolved chapter-inspect preview. `Default` shows a mixed list
 * (a missing name → number only, a null number → "—"); `Empty` shows the zero
 * count. Flip the Storybook theme toolbar to confirm both themes.
 */
const meta = {
  title: 'Import/ChapterInspectList',
  component: ChapterInspectList,
  parameters: { layout: 'padded' },
  decorators: [() => ({ template: '<div style="max-width:640px"><story /></div>' })],
} satisfies Meta<typeof ChapterInspectList>

export default meta
type Story = StoryObj<typeof meta>

/** A sample preview — exercises the missing-name + null-number gaps. */
export const Default: Story = {
  args: { chapters: inspectChapters },
}

/** A source that reports no chapters yet. */
export const Empty: Story = {
  args: { chapters: [] },
}
