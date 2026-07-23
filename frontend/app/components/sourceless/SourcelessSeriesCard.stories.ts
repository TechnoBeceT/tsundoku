import type { Meta, StoryObj } from '@storybook/vue3'
import SourcelessSeriesCard from './SourcelessSeriesCard.vue'
import { sampleSourcelessSeries } from '../../fixtures/sourceless'

/**
 * Stories for one row of the Sourceless page. The card shows the series
 * identity, a single "N sourceless chapters" count, and a "Review" button that
 * opens the per-series cleanup dialog. Flip the theme toolbar to check both
 * themes.
 */
const meta = {
  title: 'Sourceless/SourcelessSeriesCard',
  component: SourcelessSeriesCard,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof SourcelessSeriesCard>

export default meta
type Story = StoryObj<typeof meta>

const first = sampleSourcelessSeries[0]!

/** Default — a few sourceless chapters, ready to review. */
export const Default: Story = {
  args: {
    row: first,
    busy: false,
  },
}

/** A series with a large sourceless count. */
export const HighCount: Story = {
  args: {
    row: { ...first, sourcelessCount: 128 },
    busy: false,
  },
}

/** The review/removal flow is in flight — the button spins and blocks re-clicks. */
export const Busy: Story = {
  args: {
    row: first,
    busy: true,
  },
}
