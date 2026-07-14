import type { Meta, StoryObj } from '@storybook/vue3'
import MetadataCandidateCard from './MetadataCandidateCard.vue'
import { metadataCandidates } from '../../fixtures/seriesDetail'

/**
 * Stories for MetadataCandidateCard — the selectable search-result card in the
 * "Identify" match flow. A DESIGN EXPLORATION for visual sign-off: the selected
 * (accent ring + check) vs unselected treatment, each provider badge, the
 * no-cover placeholder, a long clamped title, and the grid density.
 *
 * All data is driven via the `candidate`/`selected` args (never hardcoded slot
 * text). Flip the theme toolbar to check dark vs light on any story.
 */
const [aniList, mangaDex, mangaUpdates, , mal, , noCover, longTitle] = metadataCandidates

const meta = {
  title: 'SeriesDetail/MetadataCandidateCard',
  component: MetadataCandidateCard,
  parameters: { layout: 'padded' },
  argTypes: { selected: { control: 'boolean' } },
  args: { candidate: aniList, selected: false },
  // A single card is narrow; cap the width so it reads at real grid size.
  render: (args) => ({
    components: { MetadataCandidateCard },
    setup: () => ({ args }),
    template: '<div style="max-width:150px"><MetadataCandidateCard v-bind="args" /></div>',
  }),
} satisfies Meta<typeof MetadataCandidateCard>

export default meta
type Story = StoryObj<typeof meta>

/** Unselected — the resting card. */
export const Default: Story = {}

/** Selected — the accent ring/border + the check badge on the cover. */
export const Selected: Story = {
  args: { selected: true },
}

/**
 * Ranked — a multi-select pick showing its 1-based order (rank 2 here) instead
 * of a plain checkmark, so the owner can see WHICH picks are primary vs
 * secondary in the merge.
 */
export const Ranked: Story = {
  args: { selected: true, rank: 2 },
}

/** MangaDex provider badge. */
export const MangaDexProvider: Story = {
  args: { candidate: mangaDex },
}

/** MangaUpdates provider badge. */
export const MangaUpdatesProvider: Story = {
  args: { candidate: mangaUpdates },
}

/** MAL provider badge. */
export const MalProvider: Story = {
  args: { candidate: mal },
}

/** No cover URL — the initial-letter placeholder fills the cover box. */
export const NoCover: Story = {
  args: { candidate: noCover },
}

/** A very long provider title — proves the 2-line clamp holds the card height. */
export const LongTitle: Story = {
  args: { candidate: longTitle, selected: true },
}

/**
 * A small grid of cards — the density the modal renders at, with TWO selected
 * (multi-select merge) showing their pick order (1 = primary, 2 = secondary),
 * so the whole picker reads at a glance.
 */
export const Grid: Story = {
  render: () => ({
    components: { MetadataCandidateCard },
    setup: () => ({
      candidates: metadataCandidates.slice(0, 6),
      selectedIds: [metadataCandidates[0]!.id, metadataCandidates[2]!.id],
    }),
    template:
      '<div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;max-width:440px">' +
      '<MetadataCandidateCard v-for="c in candidates" :key="c.id" :candidate="c" '
      + ':selected="selectedIds.includes(c.id)" :rank="selectedIds.indexOf(c.id) + 1 || undefined" />' +
      '</div>',
  }),
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so it renders light
 * regardless of the toolbar.
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { MetadataCandidateCard },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="background:var(--bg);padding:24px;border-radius:18px;max-width:198px">' +
      '<MetadataCandidateCard v-bind="args" />' +
      '</div>',
  }),
  args: { selected: true },
}
