import type { Meta, StoryObj } from '@storybook/vue3'
import PurgeSourceDialog from './PurgeSourceDialog.vue'

/**
 * Stories for the purge-source confirm dialog (a destructive ConfirmModal). It
 * removes ALL of Tsundoku's DB state for one source — its provider links, feeds,
 * metrics, and breaker state — while KEEPING every downloaded CBZ. The dialog
 * shows a dry-run preview of the blast radius before the owner confirms. The page
 * owns the open state and closes it ONLY once the purge succeeded, so the failure
 * story (dialog open, error inside) is a real state. Flip the theme toolbar to
 * confirm both themes.
 */
const meta = {
  title: 'Health/PurgeSourceDialog',
  component: PurgeSourceDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof PurgeSourceDialog>

export default meta
type Story = StoryObj<typeof meta>

const preview = {
  sourceId: '100',
  sourceName: 'Lunar Manga',
  seriesAffected: 3,
  providers: 3,
  providerChapters: 240,
  chaptersDeleted: 2,
  metrics: 1,
  breaker: 1,
}

/** Open with the loaded preview: the blast-radius counts + confirm/cancel. */
export const Open: Story = {
  args: { open: true, sourceName: 'Lunar Manga', preview },
}

/** Still loading the preview counts. */
export const Previewing: Story = {
  args: { open: true, sourceName: 'Lunar Manga', previewing: true },
}

/** In-flight: the confirm button spins and dismissal is blocked. */
export const Busy: Story = {
  args: { open: true, sourceName: 'Lunar Manga', preview, busy: true },
}

/** A FAILED purge: the dialog STAYS open and shows the reason inside it (§16). */
export const WithError: Story = {
  args: { open: true, sourceName: 'Lunar Manga', preview, error: 'Purge failed' },
}

/** The target source can't be resolved: the generic heading, never `Purge “”?`. */
export const UnknownSource: Story = {
  args: { open: true, sourceName: '' },
}
