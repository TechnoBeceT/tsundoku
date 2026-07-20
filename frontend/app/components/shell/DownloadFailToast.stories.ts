import type { Meta, StoryObj } from '@storybook/vue3'
import DownloadFailToast from './DownloadFailToast.vue'

/**
 * Stories for DownloadFailToast — the fixed bottom-right DANGER toast surfaced by
 * DownloadFailNotifier on a `download.fail` SSE event. `layout: 'fullscreen'` so the
 * fixed-position card sits where it will in the app. Flip the theme toolbar to
 * confirm the danger palette reads on both.
 */
const meta = {
  title: 'Shell/DownloadFailToast',
  component: DownloadFailToast,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof DownloadFailToast>

export default meta
type Story = StoryObj<typeof meta>

/** A single failure with its error reason. */
export const Single: Story = {
  args: { title: 'Download failed', body: 'Cloudflare challenge failed (403)' },
}

/** An aggregated wave of failures. */
export const Aggregated: Story = {
  args: { title: '4 downloads failed', body: 'all sources exhausted their retry budget' },
}
