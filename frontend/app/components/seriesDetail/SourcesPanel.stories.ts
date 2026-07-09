import type { Meta, StoryObj } from '@storybook/vue3'
import SourcesPanel from './SourcesPanel.vue'
import { richSeries, singleProviderSeries, seriesWithUnlinkedGroup, seriesWithDuplicateProviders } from '../../fixtures/seriesDetail'

/**
 * Stories for the Series Detail "Sources" card — the count-pilled header with the
 * Add button over the importance-ranked source list (or an empty note). Flip the
 * theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/SourcesPanel',
  component: SourcesPanel,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SourcesPanel>

export default meta
type Story = StoryObj<typeof meta>

/** Three ranked sources of varied health (preferred first). */
export const Default: Story = {
  args: { providers: richSeries.providers },
}

/** A lone source — single-provider layout. */
export const SingleProvider: Story = {
  args: { providers: singleProviderSeries.providers },
}

/** No sources tracked — the empty note (the series stays in the library). */
export const Empty: Story = {
  args: { providers: [] },
}

/** Saving: reorder + remove disabled across every row. */
export const Saving: Story = {
  args: { providers: richSeries.providers, saving: true },
}

/** A library-imported unlinked disk-group alongside linked sources — the "Match to source" row action. */
export const WithUnlinkedGroup: Story = {
  args: { providers: seriesWithUnlinkedGroup.providers },
}

/** A drifted duplicate pair — the banner + "Clean up" and per-row DUPLICATE badge. */
export const WithDuplicates: Story = {
  args: {
    providers: seriesWithDuplicateProviders.providers,
    driftedIds: [seriesWithDuplicateProviders.providers.find(p => !p.linked)!.id],
  },
}
