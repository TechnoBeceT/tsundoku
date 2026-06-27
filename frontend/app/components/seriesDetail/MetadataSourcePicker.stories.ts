import type { Meta, StoryObj } from '@storybook/vue3'
import MetadataSourcePicker from './MetadataSourcePicker.vue'
import { richSeries } from '../../fixtures/seriesDetail'

/**
 * Stories for the (PLANNED) metadata-source picker — one selectable card per
 * source, the active one carrying the accent border + check. Flip the theme
 * toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/MetadataSourcePicker',
  component: MetadataSourcePicker,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof MetadataSourcePicker>

export default meta
type Story = StoryObj<typeof meta>

const providers = richSeries.providers
const preferredId = providers[0]!.id

/** Auto (unpinned): the preferred source is active + tagged "Preferred · default". */
export const Default: Story = {
  args: {
    providers,
    title: richSeries.title,
    activeId: preferredId,
    preferredId,
  },
}

/** A non-preferred source pinned as the active metadata source. */
export const PinnedSecondary: Story = {
  args: {
    providers,
    title: richSeries.title,
    activeId: providers[1]!.id,
    preferredId,
  },
}
