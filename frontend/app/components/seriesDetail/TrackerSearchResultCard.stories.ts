import type { Meta, StoryObj } from '@storybook/vue3'
import TrackerSearchResultCard from './TrackerSearchResultCard.vue'
import { trackSearchResults } from '../../fixtures/seriesDetail'
import type { TrackSearchResult } from '../screens/seriesDetail.types'

/**
 * Stories for TrackerSearchResultCard — one "Add tracking" search hit, the
 * Komikku-style rich card (`CoverImage` + title + type/started/status meta +
 * community score + description snippet). `Thin`/`NoCover` prove the
 * best-effort enrichment fields degrade gracefully when a tracker's search
 * response leaves them at "" / 0 (spec: never fabricated).
 */
const meta = {
  title: 'SeriesDetail/TrackerSearchResultCard',
  component: TrackerSearchResultCard,
  parameters: { layout: 'padded' },
  args: {
    result: trackSearchResults[0]!,
  },
} satisfies Meta<typeof TrackerSearchResultCard>

export default meta
type Story = StoryObj<typeof meta>

/** Default — full rich card: cover, type/started/status, score, description. */
export const Default: Story = {}

/** Thin — no cover, no enrichment fields (the fixture's second result). */
export const Thin: Story = {
  args: { result: trackSearchResults[1]! },
}

/** NoCover — enrichment present, but no thumbnail (placeholder tile). */
export const NoCover: Story = {
  args: { result: { ...trackSearchResults[0]!, coverUrl: '' } satisfies TrackSearchResult },
}

/** Bind in flight — the Bind button spins. */
export const Binding: Story = {
  args: { busy: true },
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so the card renders
 * light regardless of the toolbar toggle.
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { TrackerSearchResultCard },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="padding:20px;background:var(--bg);max-width:360px"><TrackerSearchResultCard v-bind="args" /></div>',
  }),
}
