import type { Meta, StoryObj } from '@storybook/vue3'
import DownloadCounter from './DownloadCounter.vue'

/**
 * Stories for DownloadCounter — the always-visible download trio pinned at the foot
 * of the nav rail (downloading blue / queued yellow / failed red). It is only ~42px
 * wide, so it renders against a dark rail-like backdrop here. Flip the theme toolbar
 * to confirm the colour-coding reads in both.
 */
const meta = {
  title: 'Shell/DownloadCounter',
  component: DownloadCounter,
  parameters: { layout: 'centered', backgrounds: { default: 'rail' } },
} satisfies Meta<typeof DownloadCounter>

export default meta
type Story = StoryObj<typeof meta>

/** A busy library: work in every bucket. */
export const Busy: Story = {
  args: { downloading: 3, queued: 42, failed: 2 },
}

/** Only failures need attention — the red count is the one that pops. */
export const OnlyFailures: Story = {
  args: { downloading: 0, queued: 0, failed: 5 },
}

/** Fully idle — the trio is present but dimmed (never collapses/reflows). */
export const Idle: Story = {
  args: { downloading: 0, queued: 0, failed: 0 },
}

/** Actively downloading with a deep queue behind it. */
export const Downloading: Story = {
  args: { downloading: 5, queued: 128, failed: 0 },
}
