import type { Meta, StoryObj } from '@storybook/vue3'
import SourceSeriesPanel from './SourceSeriesPanel.vue'
import type { SourceSeriesRow } from './sourceSeries.types'
// Load this screen's status tokens directly (index.css does not @import them yet)
// so every story renders with the real palette in both themes.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the per-source "what depends on this source" impact panel
 * (QCAT-513). Presentation-only (rows + §16 state in), so every state is a pure
 * fixture: a source whose series all keep an alternative, one where every series
 * goes dark, a mixed set, plus the loading, error, and empty states. Flip the
 * theme toolbar for dark/light.
 */
const withAlternatives: SourceSeriesRow[] = [
  { seriesId: 's-1', title: 'Solo Leveling', alternativeCount: 2, goesDark: false, topAlternative: 'Flame Comics' },
  { seriesId: 's-2', title: 'The Beginning After the End', alternativeCount: 1, goesDark: false, topAlternative: 'Asura Scans' },
  { seriesId: 's-3', title: 'Omniscient Reader', alternativeCount: 3, goesDark: false, topAlternative: 'Hive Scans' },
]

const allDark: SourceSeriesRow[] = [
  { seriesId: 's-4', title: 'Only Here', alternativeCount: 0, goesDark: true, topAlternative: '' },
  { seriesId: 's-5', title: 'Last Copy Standing', alternativeCount: 0, goesDark: true, topAlternative: '' },
]

const mixed: SourceSeriesRow[] = [...withAlternatives, ...allDark]

const meta = {
  title: 'Settings/SourceSeriesPanel',
  component: SourceSeriesPanel,
  parameters: { layout: 'padded' },
  args: { rows: mixed, pending: false, error: null },
} satisfies Meta<typeof SourceSeriesPanel>

export default meta
type Story = StoryObj<typeof meta>

/** Every series keeps at least one alternative provider — none goes dark. */
export const HasAlternatives: Story = {
  args: { rows: withAlternatives },
}

/** Every series' only provider is this source — all go dark on pause. */
export const GoesDark: Story = {
  args: { rows: allDark },
}

/** A mix — some series take over cleanly, some go dark. */
export const Mixed: Story = {
  args: { rows: mixed },
}

/** The list is loading (§16). */
export const Loading: Story = {
  args: { rows: [], pending: true },
}

/** A load failure, surfaced inline (§16). */
export const LoadError: Story = {
  args: { rows: [], error: 'The affected-series list could not be loaded' },
}

/** Nothing carries the source — pausing changes nothing. */
export const Empty: Story = {
  args: { rows: [] },
}
