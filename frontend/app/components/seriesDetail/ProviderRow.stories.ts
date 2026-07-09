import type { Meta, StoryObj } from '@storybook/vue3'
import ProviderRow from './ProviderRow.vue'
import { richSeries, unlinkedProvider } from '../../fixtures/seriesDetail'

/**
 * Stories for one ranked source row — the ReorderControl rank stepper, the
 * health badge, the language chip, and the quiet Remove button. Sources come
 * from the shared fixture. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/ProviderRow',
  component: ProviderRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ProviderRow>

export default meta
type Story = StoryObj<typeof meta>

/** The preferred (rank-1) source: PREFERRED chip, healthy, up arrow disabled. */
export const Preferred: Story = {
  args: {
    provider: richSeries.providers[0]!,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
  },
}

/** A stale middle source: both arrows enabled, "N behind" note. */
export const Stale: Story = {
  args: {
    provider: richSeries.providers[1]!,
    rank: 2,
    preferred: false,
    canUp: true,
    canDown: true,
  },
}

/** An erroring bottom source: erroring badge + inline last-error, down disabled. */
export const Erroring: Story = {
  args: {
    provider: richSeries.providers[2]!,
    rank: 3,
    preferred: false,
    canUp: true,
    canDown: false,
  },
}

/** Saving: reorder + remove disabled while a mutation is in flight. */
export const Saving: Story = {
  args: {
    provider: richSeries.providers[1]!,
    rank: 2,
    preferred: false,
    canUp: true,
    canDown: true,
    saving: true,
  },
}

/** An unlinked disk-origin group: UNLINKED chip, note, and the "Match to source" action. */
export const Unlinked: Story = {
  args: {
    provider: unlinkedProvider,
    rank: 4,
    preferred: false,
    canUp: true,
    canDown: false,
  },
}

/** An unlinked disk-origin group with a mergeable linked twin (drift): DUPLICATE chip alongside UNLINKED. */
export const Duplicate: Story = {
  args: {
    provider: unlinkedProvider,
    rank: 4,
    preferred: false,
    canUp: true,
    canDown: false,
    duplicate: true,
  },
}

/** mangaId === 0 (unlinked disk provider): no coverage affordance renders at all. */
export const NoCoverage: Story = {
  args: {
    provider: unlinkedProvider,
    rank: 4,
    preferred: false,
    canUp: true,
    canDown: false,
  },
}

/** mangaId > 0, coverage never fetched (`coverage` undefined): the "Show coverage" button. */
export const CoverageCollapsed: Story = {
  args: {
    provider: richSeries.providers[0]!,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
  },
}

/** Coverage loaded and a scanlator entry matches this row's own scanlator: count + ranges shown. */
export const CoverageLoaded: Story = {
  args: {
    provider: richSeries.providers[0]!,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
    coverage: [{ scanlator: 'Flame Scans', count: 42, ranges: '1-42' }],
  },
}

/** Coverage fetch resolved but failed (`null`): "Coverage unavailable". */
export const CoverageUnavailable: Story = {
  args: {
    provider: richSeries.providers[0]!,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
    coverage: null,
  },
}
