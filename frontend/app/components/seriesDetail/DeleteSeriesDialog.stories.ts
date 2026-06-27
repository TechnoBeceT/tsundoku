import type { Meta, StoryObj } from '@storybook/vue3'
import DeleteSeriesDialog from './DeleteSeriesDialog.vue'

/**
 * Stories for the required-choice delete dialog. It opens with NO choice
 * selected and the confirm button disabled — the owner must pick keep or wipe
 * before it enables, and the wipe choice flips the confirm to the danger
 * treatment. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/DeleteSeriesDialog',
  component: DeleteSeriesDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof DeleteSeriesDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Open, unselected: confirm disabled until a keep/wipe choice is made. */
export const Open: Story = {
  args: { open: true, seriesTitle: 'Solo Leveling' },
}

/** In-flight: the confirm button spins and dismissal is blocked. */
export const Busy: Story = {
  args: { open: true, seriesTitle: 'Solo Leveling', busy: true },
}
