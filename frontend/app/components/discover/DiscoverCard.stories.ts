import type { Meta, StoryObj } from '@storybook/vue3'
import { INITIAL_VIEWPORTS } from 'storybook/viewport'
import DiscoverCard from './DiscoverCard.vue'
import { popularResult } from '../../fixtures/discover'

/**
 * Stories for one Discover browse card. HOVER a card to reveal its preview popup
 * (the deliberate sibling/absolute/pointer-events-none element) and the card's
 * z-index lift — confirm the popup is never clipped or covered. `inspect`,
 * `adopt`, and `open-source-link` are logged in the Actions panel. Flip the
 * Storybook theme toolbar to confirm both themes.
 */
const meta = {
  title: 'Discover/DiscoverCard',
  component: DiscoverCard,
  parameters: {
    layout: 'centered',
    actions: { handles: ['inspect', 'adopt', 'open-source-link'] },
  },
  // Single grid cell width + headroom so the hover popup has room to render.
  decorators: [() => ({ template: '<div style="position:relative;width:200px;padding:80px 60px"><story /></div>' })],
} satisfies Meta<typeof DiscoverCard>

export default meta
type Story = StoryObj<typeof meta>

const inLibrary = popularResult.manga[0]! // Solo Leveling — cover + "IN LIBRARY" tag
const plain = popularResult.manga[1]! // Chainsaw Man — cover, not in library
const noCover = popularResult.manga[2]! // The Beginning After The End — placeholder cover

/** A card with a cover image, not yet in the library. */
export const Default: Story = {
  args: { candidate: plain },
}

/** Already adopted → the "IN LIBRARY" marker over the cover. */
export const InLibrary: Story = {
  args: { candidate: inLibrary },
}

/** No cover URL → the big faint initial-letter placeholder. */
export const NoCover: Story = {
  args: { candidate: noCover },
}

/**
 * A real mobile viewport at the narrow ~132px grid-cell width Discover.vue's
 * mobile 2-column override produces (QCAT-230/231). The hover-preview popup
 * (`DiscoverHoverPreview`) is fixed at 304px — wider than this cell — so its
 * `@media (max-width: 900px) { display: none }` rule (touch has no hover
 * anyway) is what keeps it from contributing horizontal overflow even while
 * `visibility: hidden`. Confirms the card itself renders with zero overflow
 * at the actual mobile column width, not just a narrowed desktop container.
 */
export const MobileNarrowCell: Story = {
  args: { candidate: plain },
  decorators: [() => ({ template: '<div style="position:relative;width:132px;padding:24px 12px"><story /></div>' })],
  parameters: {
    viewport: { options: INITIAL_VIEWPORTS },
  },
  globals: {
    viewport: { value: 'iphone12', isRotated: false },
  },
}
