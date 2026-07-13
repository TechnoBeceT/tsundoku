import type { Meta, StoryObj } from '@storybook/vue3'
import { INITIAL_VIEWPORTS } from 'storybook/viewport'
import RichSeriesCard from './RichSeriesCard.vue'
import {
  categoryOptions,
  richSeriesFull,
  richSeriesLong,
  richSeriesMinimal,
  richSeriesNoCover,
} from '../../fixtures/seriesDetail'

/**
 * Stories for RichSeriesCard — the Komga-style rich catalogue card. This is a
 * DESIGN EXPLORATION for visual sign-off: the two layout variants (CoverLeft vs
 * SingleColumn) are separate stories so the owner can pick by looking, plus the
 * graceful-degradation, no-cover, overflow, and light-theme cases.
 *
 * The top-right toolbar now carries the "Metadata" trigger (opens the Identify
 * modal via `openMetadata`) to the LEFT of Delete — visible in every story.
 *
 * All data is driven via the `series`/`layout` args (never hardcoded slot text).
 * Flip the theme toolbar to check dark vs light on any story; the LightTheme
 * story additionally pins a light subtree so it renders light regardless.
 */
const meta = {
  title: 'SeriesDetail/RichSeriesCard',
  component: RichSeriesCard,
  parameters: { layout: 'padded' },
  argTypes: {
    layout: {
      control: { type: 'inline-radio' },
      options: ['coverLeft', 'singleColumn'],
    },
  },
  args: { series: richSeriesFull, layout: 'coverLeft', categoryOptions },
} satisfies Meta<typeof RichSeriesCard>

export default meta
type Story = StoryObj<typeof meta>

/** Cover-left (Komga desktop shape) — the full catalogue metadata. */
export const CoverLeft: Story = {
  args: { layout: 'coverLeft' },
}

/** Single-column (narrow shape) — cover on top, text stacked below. */
export const SingleColumn: Story = {
  args: { layout: 'singleColumn' },
  render: (args) => ({
    components: { RichSeriesCard },
    setup: () => ({ args }),
    template: '<div style="max-width:460px"><RichSeriesCard v-bind="args" /></div>',
  }),
}

/**
 * Real mobile viewport — `layout="coverLeft"` (what `SeriesDetail.vue` always
 * passes; the app never switches the prop) rendered at an actual phone-width
 * VIEWPORT rather than a narrowed container. Proves the card's own
 * `@media (max-width: 900px)` rule fires on its own and renders IDENTICALLY
 * to the `SingleColumn` story above — same custom-property switches, see
 * `RichSeriesCard.vue`'s `<style>` — with no horizontal overflow at any width.
 */
export const MobileViewport: Story = {
  args: { layout: 'coverLeft' },
  parameters: {
    viewport: { options: INITIAL_VIEWPORTS },
  },
  globals: {
    viewport: { value: 'iphone12', isRotated: false },
  },
}

/** Data-poor series — synopsis/genres/tags/links/authors all absent, card stays tidy. */
export const MinimalData: Story = {
  args: { series: richSeriesMinimal },
}

/** No cover URL — the branded placeholder fills the cover box. */
export const NoCover: Story = {
  args: { series: richSeriesNoCover },
}

/** Overflow stress — long title/alts/synopsis and large chip + link sets. */
export const LongEverything: Story = {
  args: { series: richSeriesLong },
}

/** Long everything in the narrow single-column shape. */
export const LongSingleColumn: Story = {
  args: { series: richSeriesLong, layout: 'singleColumn' },
  render: (args) => ({
    components: { RichSeriesCard },
    setup: () => ({ args }),
    template: '<div style="max-width:460px"><RichSeriesCard v-bind="args" /></div>',
  }),
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so it renders light
 * regardless of the toolbar (the tokens re-scope under any element carrying the
 * attribute, not just <html>).
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { RichSeriesCard },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="background:var(--bg);padding:24px;border-radius:18px"><RichSeriesCard v-bind="args" /></div>',
  }),
}
