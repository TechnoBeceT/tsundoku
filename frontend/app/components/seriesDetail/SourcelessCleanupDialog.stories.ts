import type { Meta, StoryObj } from '@storybook/vue3'
import SourcelessCleanupDialog from './SourcelessCleanupDialog.vue'
import { sampleSourcelessPreview } from '../../fixtures/sourceless'

/**
 * Stories for the per-series sourceless-cleanup dialog. Unlike
 * `FractionalCleanupDialog` there is no page-count yardstick: every listed
 * chapter is orphaned by definition (no remaining source can satisfy it), so
 * every row starts pre-ticked and the owner opts OUT rather than in. The
 * destructive delete only ever fires through the shared `ConfirmModal`
 * (QCAT-222) — click "Delete N files" to see it.
 */
const meta = {
  title: 'SeriesDetail/SourcelessCleanupDialog',
  component: SourcelessCleanupDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof SourcelessCleanupDialog>

export default meta
type Story = StoryObj<typeof meta>

/** The default 3-chapter removable set, all pre-ticked. */
export const Default: Story = {
  args: { open: true, seriesTitle: 'Solo Leveling', preview: sampleSourcelessPreview },
}

/** Nothing removable — the empty state, confirm disabled at zero. */
export const Empty: Story = {
  args: { open: true, seriesTitle: 'Solo Leveling', preview: { chapters: [] } },
}

/** In-flight: the trigger + checkboxes are locked, dismissal is blocked (§16). */
export const Busy: Story = {
  args: { open: true, seriesTitle: 'Solo Leveling', preview: sampleSourcelessPreview, busy: true },
}

/** A FAILED removal: the dialog STAYS open with the reason inside it (§16). */
export const WithError: Story = {
  args: { open: true, seriesTitle: 'Solo Leveling', preview: sampleSourcelessPreview, error: 'Update failed' },
}
