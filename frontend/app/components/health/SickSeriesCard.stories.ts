import type { Meta, StoryObj } from '@storybook/vue3'
import SickSeriesCard from './SickSeriesCard.vue'
import { sickSeries } from '../../fixtures/libraryHealth'
// Load the Series Detail health-badge tokens directly: index.css does not @import
// them yet (a coordinator wires that line to avoid parallel-worker conflicts), so
// the side-effect import keeps every story rendering with the real health palette.
import '../../assets/css/tokens/seriesDetail.css'

/**
 * Stories for one sick-series card — the clickable header (cover · title · "N
 * unhealthy sources") plus its list of unhealthy source rows. Series are pulled
 * from the shared health fixture. Clicking the header emits `open-series`. Flip
 * the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Health/SickSeriesCard',
  component: SickSeriesCard,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SickSeriesCard>

export default meta
type Story = StoryObj<typeof meta>

/** A series with a single unhealthy source. */
export const SingleSource: Story = {
  args: { series: sickSeries[1]! },
}

/** A series with multiple unhealthy sources (stale + erroring mix). */
export const MultipleSources: Story = {
  args: { series: sickSeries[0]! },
}
