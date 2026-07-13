import type { Meta, StoryObj } from '@storybook/vue3'
import CoverPickerModal from './CoverPickerModal.vue'
import { coverCandidates, currentCoverId } from '../../fixtures/seriesDetail'

/**
 * Stories for CoverPickerModal — the "Choose cover" gallery. A DESIGN
 * EXPLORATION for visual sign-off. The modal is a SINGLE view (a cover-first
 * gallery over a set of candidate posters), so every state is a pure prop
 * variation — NO `play` interaction is needed (and none is used: reka portals
 * the dialog to `document.body`, outside the story canvas, so a canvas-scoped
 * `play` could never reach it anyway).
 *
 * Every required prop is driven via args/fixtures; single-select + confirm are
 * local, so pick any tile to see the accent ring/check + enabled "Use this
 * cover". Flip the theme toolbar to confirm both themes on any story; LightTheme
 * additionally pins a light subtree.
 */
const meta = {
  title: 'SeriesDetail/CoverPickerModal',
  component: CoverPickerModal,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    candidates: coverCandidates,
    currentId: undefined,
    loading: false,
  },
} satisfies Meta<typeof CoverPickerModal>

export default meta
type Story = StoryObj<typeof meta>

/** Default — the populated cover gallery, single-select. */
export const Default: Story = {}

/**
 * WithCurrent — the series' current cover is marked "Current" and preselected on
 * open (accent ring + check on that tile).
 */
export const WithCurrent: Story = {
  args: { currentId: currentCoverId },
}

/** Loading — a skeleton cover grid while candidates are fetched. */
export const Loading: Story = {
  args: { loading: true },
}

/** Empty — no provider offered a cover (§16 empty state). */
export const Empty: Story = {
  args: { candidates: [] },
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so the gallery renders
 * light regardless of the toolbar. Args-driven; no interaction.
 */
export const LightTheme: Story = {
  args: { currentId: currentCoverId },
  render: (args) => ({
    components: { CoverPickerModal },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="min-height:100vh;background:var(--bg)"><CoverPickerModal v-bind="args" /></div>',
  }),
}
