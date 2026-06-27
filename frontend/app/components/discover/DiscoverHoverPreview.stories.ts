import type { Meta, StoryObj } from '@storybook/vue3'
import DiscoverHoverPreview from './DiscoverHoverPreview.vue'
import { popularResult } from '../../fixtures/discover'

/**
 * Stories for the Discover hover-preview popup. In the app it is hidden until its
 * DiscoverCard is hovered; here it is forced open with the `visible` prop so its
 * cover header, source line, description, and genre <Chip>s can be inspected.
 * Because the popup is `position:absolute`, each story anchors it inside a small
 * relative box. Flip the Storybook theme toolbar to confirm both themes.
 */
const meta = {
  title: 'Discover/DiscoverHoverPreview',
  component: DiscoverHoverPreview,
  parameters: { layout: 'centered' },
  // The popup is absolutely-positioned; give it a relative anchor with headroom.
  decorators: [() => ({ template: '<div style="position:relative;width:304px;height:420px"><story /></div>' })],
} satisfies Meta<typeof DiscoverHoverPreview>

export default meta
type Story = StoryObj<typeof meta>

// A few representative candidates from the Discover fixture.
const withCover = popularResult.manga[0]! // Solo Leveling — in-library, cover, genres
const noCover = popularResult.manga[2]! // The Beginning After The End — placeholder cover
const bare = popularResult.manga[6]! // Berserk — no description / genres (graceful fallback)

/** Full popup: image cover, "In library" marker, description, genre chips. */
export const Default: Story = {
  args: { candidate: withCover, visible: true },
}

/** No cover URL → the big faint initial-letter placeholder. */
export const NoCover: Story = {
  args: { candidate: noCover, visible: true },
}

/** No description / genres → the graceful empty-description fallback, no chips. */
export const Minimal: Story = {
  args: { candidate: bare, visible: true },
}
