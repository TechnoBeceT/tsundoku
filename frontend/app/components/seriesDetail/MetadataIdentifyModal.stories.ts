import type { Meta, StoryObj } from '@storybook/vue3'
import MetadataIdentifyModal from './MetadataIdentifyModal.vue'
import { metadataCandidates } from '../../fixtures/seriesDetail'

/**
 * Stories for MetadataIdentifyModal — the manual-correction "Identify" match
 * tool. A DESIGN EXPLORATION for visual sign-off. The modal is a SINGLE view
 * (editable Title field + inline Search over a candidate gallery), so every
 * state is a pure prop variation — NO `play` interaction is needed (and none is
 * used: reka portals the dialog to `document.body`, outside the story canvas, so
 * a canvas-scoped `play` could never reach it anyway).
 *
 * Every required prop is driven via args/fixtures. Flip the theme toolbar to
 * confirm both themes on any story; LightTheme additionally pins a light subtree.
 */
const meta = {
  title: 'SeriesDetail/MetadataIdentifyModal',
  component: MetadataIdentifyModal,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    title: 'Dragon Slayer’s Regression',
    candidates: metadataCandidates,
    loading: false,
  },
} satisfies Meta<typeof MetadataIdentifyModal>

export default meta
type Story = StoryObj<typeof meta>

/** Default — the Title field prefilled over a populated candidate gallery. */
export const Default: Story = {}

/** Results — the populated candidate grid, single-select (alias of Default). */
export const Results: Story = {}

/** Loading — a skeleton grid while a search is in flight. */
export const Loading: Story = {
  args: { loading: true },
}

/** Empty — a search that matched nothing (§16 empty state). */
export const Empty: Story = {
  args: { candidates: [] },
}

/** Selected — a candidate preselected via an initial-slice fixture is not
 *  possible from props alone (selection is local), so this renders the same
 *  gallery; pick any card to see the selected treatment + enabled Confirm. */
export const Selected: Story = {}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so the candidate
 * cards render light. Args-driven; no interaction.
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { MetadataIdentifyModal },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="min-height:100vh;background:var(--bg)"><MetadataIdentifyModal v-bind="args" /></div>',
  }),
}
