import type { Meta, StoryObj } from '@storybook/vue3'
import SetChapterProgressDialog from './SetChapterProgressDialog.vue'

/**
 * Stories for SetChapterProgressDialog — the QCAT-242 entry-point-B confirm
 * (a chapter row's "Set as current progress" action). A thin `ConfirmModal`
 * wrapper, mirroring `RemoveSourceDialog`'s own stories shape.
 */
const meta = {
  title: 'SeriesDetail/SetChapterProgressDialog',
  component: SetChapterProgressDialog,
  parameters: { layout: 'padded' },
  args: {
    open: true,
    busy: false,
    chapterNumber: 42,
    error: null,
  },
} satisfies Meta<typeof SetChapterProgressDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Default — confirms jumping the series (local chapters + every bound tracker) to chapter 42. */
export const Default: Story = {}

/** Confirm in flight — the Set progress button spins and dismissal is blocked. */
export const Busy: Story = {
  args: { busy: true },
}

/** §16: a failed reset — the backend's real 4xx message renders inline; the dialog stays open. */
export const Failed: Story = {
  args: { error: 'anilist: rate limited — try again shortly.' },
}

/** The target chapter couldn't be resolved (a vanished/mid-refresh row) — the generic heading fallback. */
export const UnresolvedTarget: Story = {
  args: { chapterNumber: null },
}
