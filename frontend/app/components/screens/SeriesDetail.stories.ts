import type { Meta, StoryObj } from '@storybook/vue3'
import { reactive } from 'vue'
import { userEvent, within } from 'storybook/test'
import SeriesDetail from './SeriesDetail.vue'
import type { SeriesDetail as SeriesDetailData } from './seriesDetail.types'
// The badge palette tokens this screen needs. Loaded here so Storybook renders
// the state/health hues — in the app the same file is @import'd by index.css.
import '../../assets/css/tokens/seriesDetail.css'
import {
  categoryOptions,
  noCoverSeries,
  richSeries,
  seriesWithUnlinkedGroup,
  singleProviderSeries,
  trackBindings,
} from '../../fixtures/seriesDetail'
import { trackers } from '../../fixtures/settings'

/**
 * Stories for the Series Detail screen. Flip the Storybook theme toolbar to
 * confirm it reads correctly in BOTH dark and light. `Default` is interactive
 * (toggles, category, reorder, metadata-source and delete all mutate local state
 * so the round-trip is visible); `DeleteDialogOpen` opens the required-choice
 * dialog via a play function; `SingleProvider` and `NoCover` are static variants.
 */
const meta = {
  title: 'Screens/SeriesDetail',
  component: SeriesDetail,
  parameters: { layout: 'fullscreen' },
  // series/categoryOptions are required props; the interactive stories pass their
  // own in the render template, so these defaults only satisfy the CSF3 typing.
  args: { series: richSeries, categoryOptions },
} satisfies Meta<typeof SeriesDetail>

export default meta
type Story = StoryObj<typeof meta>

// A live wrapper: the screen is props-in/emits-out, so the story owns the state
// and applies every emitted mutation back onto the `series` prop — proving the
// §16 success round-trip (no separate refetch).
const interactive = (initial: SeriesDetailData) => ({
  components: { SeriesDetail },
  setup() {
    const series = reactive<SeriesDetailData>(structuredClone(initial))
    const onCategory = (c: string): void => { series.category = c }
    const onMonitored = (v: boolean): void => { series.monitored = v }
    const onCompleted = (v: boolean): void => { series.completed = v }
    const onReorder = (next: { id: string, importance: number }[]): void => {
      for (const { id, importance } of next) {
        const p = series.providers.find((x) => x.id === id)
        if (p) p.importance = importance
      }
    }
    // The screen only REQUESTS a removal (the confirm dialog lives on the page,
    // which closes it on a successful mutation) — the story stands in for the
    // page by applying the removal straight away.
    const onRemove = (id: string): void => {
      series.providers = series.providers.filter((p) => p.id !== id)
    }
    return { series, categoryOptions, trackBindings, trackers, onCategory, onMonitored, onCompleted, onReorder, onRemove }
  },
  template: `
    <SeriesDetail
      :series="series"
      :category-options="categoryOptions"
      :track-bindings="trackBindings"
      :trackers="trackers"
      @change-category="onCategory"
      @toggle-monitored="onMonitored"
      @toggle-completed="onCompleted"
      @reorder-providers="onReorder"
      @request-remove-source="onRemove"
    />
  `,
})

/** A rich series: every chapter state, three providers of varied health. */
export const Default: Story = {
  render: () => interactive(richSeries),
}

/** The required-choice delete dialog, opened by clicking the Delete button. */
export const DeleteDialogOpen: Story = {
  render: () => interactive(richSeries),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: 'Delete' }))
  },
}

/** A series tracked on a single source — the lone-provider layout. */
export const SingleProvider: Story = {
  render: () => interactive(singleProviderSeries),
}

/** No cover URL — the branded placeholder fills the header + meta cards. */
export const NoCover: Story = {
  render: () => interactive(noCoverSeries),
}

/** A library-imported unlinked disk-group alongside linked sources — the "Match to source" row action. */
export const WithUnlinkedGroup: Story = {
  render: () => interactive(seriesWithUnlinkedGroup),
}
