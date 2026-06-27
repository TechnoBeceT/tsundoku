import type { Meta, StoryObj } from '@storybook/vue3'
import SeriesCard from './SeriesCard.vue'
import { seriesPage } from '../../fixtures/series'

/**
 * Stories for the library grid card. Each story frames the card at its grid
 * width so the portrait cover reads; flip the Storybook theme toolbar to confirm
 * the on-cover badge, flags, and progress bar hold up on both surfaces.
 */
const meta = {
  title: 'Library/SeriesCard',
  component: SeriesCard,
  parameters: { layout: 'centered' },
  decorators: [
    () => ({ template: '<div style="width:200px"><story /></div>' }),
  ],
} satisfies Meta<typeof SeriesCard>

export default meta
type Story = StoryObj<typeof meta>

/** A monitored, in-progress series with a real cover + wanted/failed counts. */
export const Default: Story = {
  args: { series: seriesPage[0] },
}

/** No cover URL → the branded placeholder; also paused + completed. */
export const Placeholder: Story = {
  args: { series: seriesPage[3] },
}

/** Freshly adopted: nothing downloaded yet (0% bar, all chapters wanted). */
export const FreshlyAdopted: Story = {
  args: { series: seriesPage[5] },
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
