import type { Meta, StoryObj } from '@storybook/vue3'
import UnhealthySourceRow from './UnhealthySourceRow.vue'
import { sickSeries } from '../../fixtures/libraryHealth'
// Load the Series Detail health-badge tokens directly: index.css does not @import
// them yet (a coordinator wires that line to avoid parallel-worker conflicts), so
// the side-effect import keeps every story rendering with the real health palette.
import '../../assets/css/tokens/seriesDetail.css'

/**
 * Stories for a single unhealthy source row — the line that appears under each
 * sick series in Library Health. Sources are pulled from the shared health
 * fixture so the badges/labels match the screen. Flip the Storybook theme
 * toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Health/UnhealthySourceRow',
  component: UnhealthySourceRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof UnhealthySourceRow>

export default meta
type Story = StoryObj<typeof meta>

/** An erroring source: erroring badge + inline last-error + behind count. */
export const Erroring: Story = {
  args: { source: sickSeries[0]!.sources[0]! },
}

/** A stale source: stale badge, behind count, no error. */
export const Stale: Story = {
  args: { source: sickSeries[0]!.sources[1]! },
}

/** A source that has never synced ("never synced" label) and is behind. */
export const NeverSynced: Story = {
  args: { source: sickSeries[2]!.sources[1]! },
}
