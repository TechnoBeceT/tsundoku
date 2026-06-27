import type { Meta, StoryObj } from '@storybook/vue3'
import LibraryHealth from './LibraryHealth.vue'
import { sickSeries } from '../../fixtures/libraryHealth'
// Load the Series Detail health-badge tokens directly: index.css does not @import
// them yet (a coordinator wires that line to avoid parallel-worker conflicts), so
// the side-effect import keeps every story rendering with the real health palette.
import '../../assets/css/tokens/seriesDetail.css'

/**
 * Stories for the Library Health screen — the "what needs attention" view over
 * the sick-series report. Flip the Storybook theme toolbar to confirm it reads
 * correctly in BOTH dark and light. Clicking a card emits `open-series`; the
 * rescan button emits `refresh`.
 */
const meta = {
  title: 'Screens/LibraryHealth',
  component: LibraryHealth,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof LibraryHealth>

export default meta
type Story = StoryObj<typeof meta>

/** Several sick series mixing stale + erroring sources, behind counts, errors. */
export const Default: Story = {
  args: {
    series: sickSeries,
  },
}

/** No sick series — the all-clear empty state. */
export const AllClear: Story = {
  args: {
    series: [],
  },
}

/** Rescan in flight — the button shows its spinner and is disabled. */
export const Refreshing: Story = {
  args: {
    series: sickSeries,
    refreshing: true,
  },
}
