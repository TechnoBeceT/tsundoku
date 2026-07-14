import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
import ResetProgressDialog from './ResetProgressDialog.vue'

/**
 * Stories for ResetProgressDialog — the QCAT-242 entry-point-A dialog
 * (TrackersSection's "Reset progress" header button). Every state is a pure
 * prop variation; `open: true` renders the dialog directly (it teleports via
 * `Dialog`'s reka-ui portal, so no trigger click is needed to see it).
 */
const meta = {
  title: 'SeriesDetail/ResetProgressDialog',
  component: ResetProgressDialog,
  parameters: { layout: 'padded' },
  args: {
    open: true,
    busy: false,
    error: null,
    defaultChapter: 42,
  },
} satisfies Meta<typeof ResetProgressDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Default — "Set to chapter" mode, prefilled with the series' current furthest-read. */
export const Default: Story = {}

/** From start — click the "Re-read from start" segment to hide the chapter field and show the whole-reset warning. */
export const FromStart: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('tab', { name: 'Re-read from start' }))
  },
}

/** Confirm in flight — the Apply button spins and Escape/overlay-click are blocked. */
export const Busy: Story = {
  args: { busy: true },
}

/** §16: a failed reset — the backend's real 4xx message renders inline via FormError; the dialog stays open. */
export const Failed: Story = {
  args: { error: 'anilist: rate limited — try again shortly.' },
}

/** No trackers bound yet — defaultChapter falls back to 1 (never 0, so the field never silently defaults to "from start"). */
export const NoTrackersYet: Story = {
  args: { defaultChapter: 1 },
}
