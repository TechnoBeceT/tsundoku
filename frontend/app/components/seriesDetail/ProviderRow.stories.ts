import type { Meta, StoryObj } from '@storybook/vue3'
import ProviderRow from './ProviderRow.vue'
import { richSeries, unlinkedProvider, reuploaderProvider, omakeProvider } from '../../fixtures/seriesDetail'

/**
 * Stories for one ranked source row — the ReorderControl rank stepper, the
 * health badge, the language chip, the source's chapter coverage, and the quiet
 * Remove button. Sources come from the shared fixture. Flip the theme toolbar to
 * confirm both themes.
 *
 * Coverage is shown with NO click and NO fetch: `feedCount`/`feedRanges` (what
 * the source OFFERS) come straight from the series-detail response, next to a
 * quieter "supplies N" (how many downloaded files came from this source). The
 * `FeedOffering` / `GappedFeed` / `NoFeed` stories pin exactly those three states.
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

/**
 * The headline coverage line: the source OFFERS 270 chapters (1-269) while only
 * 8 of the owner's files currently come from it — "270 chapters · 1-269" +
 * "supplies 8". Rendered with no click and no source fetch.
 */
export const FeedOffering: Story = {
  args: {
    provider: richSeries.providers[0]!,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
  },
}

/** A gapped feed: the ranges string collapses the runs ("1-88, 90-92"). */
export const GappedFeed: Story = {
  args: {
    provider: richSeries.providers[1]!,
    rank: 2,
    preferred: false,
    canUp: true,
    canDown: true,
  },
}

/**
 * A provider with no stored feed (an unlinked disk-origin group): "No chapter
 * feed" — never a phantom "0 chapters" — beside the files it still supplies.
 */
export const NoFeed: Story = {
  args: {
    provider: unlinkedProvider,
    rank: 4,
    preferred: false,
    canUp: true,
    canDown: false,
  },
}

/**
 * A fractional RE-UPLOADER, toggle OFF: the evidence line shows the long
 * SYSTEMATIC run (1.1, 2.1, 3.1 … 9.1) that gives the game away — this mirror
 * republishes every whole chapter under an "N.1" suffix. THIS is the row the
 * owner ticks the box on.
 */
export const FractionalReuploader: Story = {
  args: {
    provider: reuploaderProvider,
    rank: 2,
    preferred: false,
    canUp: true,
    canDown: true,
  },
}

/**
 * The same re-uploader with the switch ON: the list stays VISIBLE so the owner
 * can audit the decision and un-tick it. The switch suppresses; it deletes
 * nothing.
 */
export const FractionalIgnored: Story = {
  args: {
    provider: { ...reuploaderProvider, ignoreFractional: true },
    rank: 2,
    preferred: false,
    canUp: true,
    canDown: true,
  },
}

/**
 * A lone `5.5` omake: a GENUINE side-chapter, not a re-upload. `.5` is the most
 * common fractional in a real library — this is the row the owner must NOT tick,
 * and the evidence line is what tells him so.
 */
export const FractionalLoneOmake: Story = {
  args: {
    provider: omakeProvider,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
  },
}

/**
 * A clean source: no fractional chapters at all → NO evidence line and NO
 * toggle. A switch with no evidence behind it would be a blind switch.
 */
export const NoFractionals: Story = {
  args: {
    provider: richSeries.providers[0]!,
    rank: 1,
    preferred: true,
    canUp: false,
    canDown: true,
  },
}

/**
 * A pathological feed (30 fractionals): the list is capped at 12 and tails off
 * with "+18 more", while the COUNT still reports the true total.
 */
export const FractionalCapped: Story = {
  args: {
    provider: {
      ...reuploaderProvider,
      fractionalCount: 30,
      fractionalChapters: Array.from({ length: 30 }, (_, i) => `${i + 1}.1`),
    },
    rank: 2,
    preferred: false,
    canUp: true,
    canDown: true,
  },
}
