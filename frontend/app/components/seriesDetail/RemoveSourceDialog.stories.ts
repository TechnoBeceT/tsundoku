import type { Meta, StoryObj } from '@storybook/vue3'
import RemoveSourceDialog from './RemoveSourceDialog.vue'

/**
 * Stories for the remove-source confirm dialog (a destructive ConfirmModal). It
 * removes the source feed only — downloaded files + chapters are kept. The page
 * owns the open state and closes it ONLY once the removal succeeded, so the
 * failure story below (dialog still open, error inside) is a real state. Flip
 * the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/RemoveSourceDialog',
  component: RemoveSourceDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof RemoveSourceDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Open: confirm + cancel on the destructive remove prompt. */
export const Open: Story = {
  args: { open: true, sourceName: 'asurascans' },
}

/** In-flight: the confirm button spins and dismissal is blocked. */
export const Busy: Story = {
  args: { open: true, sourceName: 'asurascans', busy: true },
}

/** A FAILED removal: the dialog STAYS open and shows the reason inside it (§16). */
export const WithError: Story = {
  args: { open: true, sourceName: 'asurascans', error: 'Update failed' },
}

/** The target source can't be resolved: the generic heading, never `Remove “”?`. */
export const UnknownSource: Story = {
  args: { open: true, sourceName: '' },
}
