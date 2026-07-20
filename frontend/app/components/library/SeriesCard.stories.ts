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
const stalled = seriesPage[1]
const noUnread = seriesPage[2]
const pausedCompleted = seriesPage[3]
const needsSource = seriesPage[4]
const freshlyAdopted = seriesPage[5]
if (!monitored || !stalled || !noUnread || !pausedCompleted || !needsSource || !freshlyAdopted) {
  throw new Error('seriesPage fixture must have entries at indices 0, 1, 2, 3, 4, and 5')
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

/** `chapterCounts.unread > 0` → the unread-count badge renders in the top-right
 * corner (same fixture entry as Default, which already carries unread: 12). */
export const UnreadBadge: Story = {
  args: { series: monitored },
}

/** `chapterCounts.unread === 0` → NO badge renders at all — its absence is the
 * point, not a badge reading "0". */
export const NoUnreadBadge: Story = {
  args: { series: noUnread },
}

/** Freshly adopted: nothing downloaded yet (0% bar, all chapters wanted). */
export const FreshlyAdopted: Story = {
  args: { series: freshlyAdopted },
}

/**
 * `needsSource: true` — the amber "NEEDS SOURCE" badge renders in the top-right
 * flags column. Deliberately uses a fixture entry that ALSO has a real cover
 * (`needsSource` fixture entry), proving the badge is cover-independent
 * (handover 2026-07-13#15) — it must show up even when a cover renders.
 */
export const NeedsSource: Story = {
  args: { series: needsSource },
}

/**
 * `isStalled: true` — the amber "STALLED" badge (QCAT-297): a monitored,
 * not-completed series whose newest chapter released longer than the threshold
 * ago. Purely informational — "you're waiting and nothing has come".
 */
export const Stalled: Story = {
  args: { series: stalled },
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

/**
 * §3 container-query proof surface — the SAME card at six FIXED tile widths in
 * one frame (95 / 112 / 130 / 150 / 186 / 240px). The card is now a container,
 * so its title/badge/meta size by the TILE's width, not the viewport: a column
 * here reads a different size than its neighbour at the SAME viewport, and each
 * column reads the SAME size at any viewport (the container-query win, §3.2).
 * Desktop tiles (≥186px) hit the title cap = 13.5px (byte-identical to 2a44360).
 */
export const TileWidths: Story = {
  render: () => ({
    components: { SeriesCard },
    setup: () => ({ series: monitored, widths: [95, 112, 130, 150, 186, 240] }),
    template:
      '<div style="display:flex;gap:16px;align-items:flex-start;flex-wrap:wrap">' +
      '<div v-for="w in widths" :key="w" :style="{ width: w + \'px\' }">' +
      '<div style="font:11px monospace;color:var(--muted);margin-bottom:6px">{{ w }}px</div>' +
      '<SeriesCard :series="series" />' +
      '</div>' +
      '</div>',
  }),
}
