import type { Meta, StoryObj } from '@storybook/vue3'
import SeriesCard from './SeriesCard.vue'
import { seriesPage } from '../../fixtures/series'

/**
 * Stories for the library grid card. Each story frames the card at its grid
 * width so the portrait cover reads; flip the Storybook theme toolbar to confirm
 * the on-cover badge, flags, and progress bar hold up on both surfaces.
 */
// seriesPage is a hardcoded, non-empty fixture; pull the specific entries the
// stories showcase and assert they exist so each `series` arg is a defined
// SeriesSummary (noUncheckedIndexedAccess types a bare index as possibly-undefined).
const monitored = seriesPage[0]
const pausedCompleted = seriesPage[3]
const freshlyAdopted = seriesPage[5]
if (!monitored || !pausedCompleted || !freshlyAdopted) {
  throw new Error('seriesPage fixture must have entries at indices 0, 3, and 5')
}

const meta = {
  title: 'Library/SeriesCard',
  component: SeriesCard,
  parameters: { layout: 'centered' },
  decorators: [
    () => ({ template: '<div style="width:200px"><story /></div>' }),
  ],
  // series is a required prop; the Grid story renders its own cards, so this
  // default only satisfies the CSF3 story typing.
  args: { series: monitored },
} satisfies Meta<typeof SeriesCard>

export default meta
type Story = StoryObj<typeof meta>

/** A monitored, in-progress series with a real cover + wanted/failed counts. */
export const Default: Story = {
  args: { series: monitored },
}

/** No cover URL → the branded placeholder; also paused + completed. */
export const Placeholder: Story = {
  args: { series: pausedCompleted },
}

/** Freshly adopted: nothing downloaded yet (0% bar, all chapters wanted). */
export const FreshlyAdopted: Story = {
  args: { series: freshlyAdopted },
}

/** Every card in the fixture page, laid out in the library grid. */
export const Grid: Story = {
  render: () => ({
    components: { SeriesCard },
    setup: () => ({ items: seriesPage }),
    template:
      '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(186px,1fr));gap:18px">' +
      '<SeriesCard v-for="s in items" :key="s.id" :series="s" />' +
      '</div>',
  }),
}
